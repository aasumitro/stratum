package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/otel"
	"github.com/aasumitro/stratum/internal/platform/outbox"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

func RunWorker() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.RequireSecretsOutsideDev(); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	infra, err := bootstrap.SetupInfra(ctx, cfg, cfg.Postgres.WorkerURL)
	if err != nil {
		return err
	}
	defer infra.Close(context.Background())
	cfg.WarnPlaintextTransport(infra.Log)

	// Bucket existence is ensured by the API on its own startup (see
	// api.go) — not repeated here, since it's the same idempotent setup
	// running twice for no benefit. The worker only ever deletes existing
	// objects (HandleOrganizationDeleted), never uploads, so it doesn't
	// need EnsureBuckets to have already run before it's useful.
	var storageClient *storage.Client
	if cfg.Storage.URL != "" {
		storageClient = storage.New(storage.Config{
			BaseURL: cfg.Storage.URL,
			Key:     cfg.Auth.ServiceRoleKey,
		})
	}

	mods := bootstrap.NewWorkerModules(infra, storageClient)
	consumers := bootstrap.NewConsumers(infra.MQConn, mods, infra.Log)

	// Unregistered before infra closes (defers run in reverse order), so a final
	// metric export at shutdown does not query a pool that is already gone.
	gauges, err := registerWorkerGauges(infra.Pool, infra.MQConn.QueueMessageCount, dlqNames(consumers), infra.Log)
	if err != nil {
		infra.Log.Warn("registering worker gauges failed", "error", err)
	} else {
		defer func() { _ = gauges.Unregister() }()
	}

	// wg tracks every consumer/background goroutine below so shutdown can
	// wait for them to actually finish (Run returns once its current
	// in-flight delivery is handled and ctx.Done() is observed) instead of
	// returning from RunWorker — and letting main() exit the process —
	// while a delivery is still mid-handler. An abruptly killed handler
	// would leave its message unacked, so RabbitMQ requeues and redelivers
	// it, risking a partial double-application of whatever it was doing.
	var wg sync.WaitGroup
	for _, c := range consumers {
		wg.Go(func() { c.Run(ctx) })
	}

	// Hourly cleanup of expired organization invitations.
	wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mods.Organization.CleanupExpiredInvitations(ctx)
			}
		}
	})

	// Hourly safety net: re-enqueue an expiry check for any active/trialing
	// subscription already past its end date, in case its originally
	// scheduled SubscriptionCheck was lost. Expiry only — see
	// billing.ReconcileOverdueSubscriptions.
	wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mods.Billing.ReconcileOverdueSubscriptions(ctx)
			}
		}
	})

	// Outbox relay: delivers messaging.outbox rows (written transactionally
	// by events.Enqueue/EnqueueDelayed) to the broker. A 2-second poll, not
	// LISTEN/NOTIFY — this codebase has no such infrastructure, and a few
	// seconds of added latency is an acceptable trade for durability.
	wg.Go(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mods.OutboxRelay.RelayBatch(ctx)
			}
		}
	})

	// Hourly retention sweep: deletes published messaging.outbox rows past
	// OUTBOX_RETENTION_DAYS. Never touches unpublished rows, so a stuck or
	// exhausted row stays visible to the "Stuck outbox backlog" runbook
	// query for as long as it remains unpublished. Each module then sweeps
	// its own schema.
	sweeps := moduleRetentionSweeps(mods, cfg)
	sweepFailures := otel.Counter("stratum.retention.sweep.failures", "Retention sweeps that failed and will retry on the next tick.")
	wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := outbox.Sweep(ctx, infra.Pool, cfg.OutboxRetentionDays); err != nil {
					infra.Log.Warn("outbox retention sweep failed", "error", err)
				}
				runRetentionSweeps(ctx, infra.Log, sweepFailures, sweeps)
			}
		}
	})

	infra.Log.Info("worker running", "consumers", len(consumers))
	<-ctx.Done()
	infra.Log.Info("worker shutting down, waiting for in-flight deliveries to finish")
	wg.Wait()
	infra.Log.Info("worker shutdown complete")
	return nil
}

// retentionSweep is one module's hourly retention pass.
type retentionSweep struct {
	module string
	run    func(context.Context) (int64, error)
}

func moduleRetentionSweeps(mods *bootstrap.WorkerModules, cfg *config.Config) []retentionSweep {
	return []retentionSweep{
		{"account", func(ctx context.Context) (int64, error) {
			return mods.Account.SweepRetention(ctx, cfg.LoginEventRetentionDays, cfg.DataExportRetentionDays)
		}},
		{"notification", func(ctx context.Context) (int64, error) {
			return mods.Notification.SweepRetention(ctx, cfg.NotificationRetentionDays)
		}},
		{"organization", func(ctx context.Context) (int64, error) {
			return mods.Organization.SweepRetention(ctx, cfg.WebhookDeliveryRetentionDays, cfg.DeletedOrganizationRetentionDays)
		}},
		{"billing", func(ctx context.Context) (int64, error) {
			return mods.Billing.SweepRetention(ctx, cfg.WebhookEventRetentionDays)
		}},
	}
}

// runRetentionSweeps runs every sweep regardless of earlier failures: a failed one
// logs, counts and retries on the next tick, and must not stop the others. A failure
// is counted because data outliving its documented retention is a compliance gap
// someone needs to hear about. Shutdown is not a failure: it stops the loop quietly.
// What was removed is logged, which is the evidence retention actually ran.
func runRetentionSweeps(ctx context.Context, log *slog.Logger, failures metric.Int64Counter, sweeps []retentionSweep) {
	for _, sweep := range sweeps {
		removed, err := sweep.run(ctx)
		if ctx.Err() != nil {
			return
		}
		if removed > 0 {
			log.Info("retention sweep removed rows", "module", sweep.module, "rows", removed)
		}
		if err != nil {
			failures.Add(ctx, 1, metric.WithAttributes(attribute.String("module", sweep.module)))
			log.Warn("retention sweep failed", "module", sweep.module, "error", err)
		}
	}
}

// dlqNames lists each dead-letter queue the consumers declare, once. Reading them
// from the consumer specs keeps the gauge in step with the real topology.
func dlqNames(consumers []*messaging.Consumer) []string {
	names := make([]string, 0, len(consumers))
	for _, c := range consumers {
		names = append(names, c.Spec().DLX.QueueName)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// registerWorkerGauges reports stalled delivery, abandoned events and dead-lettered
// messages, the failure states nothing else pushes to an operator. A source that
// fails to answer is logged and skipped, so one outage does not blank the others.
func registerWorkerGauges(
	pool *pgxpool.Pool, queueMessages func(queue string) (int, error),
	dlqs []string, log *slog.Logger,
) (metric.Registration, error) {
	meter := otel.Meter()
	due, err := meter.Int64ObservableGauge("stratum.outbox.due_unpublished",
		metric.WithDescription("Outbox rows the relay still owes: due, or failed and waiting to retry."))
	if err != nil {
		return nil, fmt.Errorf("app.registerWorkerGauges: %w", err)
	}
	exhausted, err := meter.Int64ObservableGauge("stratum.outbox.exhausted",
		metric.WithDescription("Outbox rows abandoned after every retry; only an operator can recover them."))
	if err != nil {
		return nil, fmt.Errorf("app.registerWorkerGauges: %w", err)
	}
	depth, err := meter.Int64ObservableGauge("stratum.dlq.messages",
		metric.WithDescription("Messages waiting in a dead-letter queue."))
	if err != nil {
		return nil, fmt.Errorf("app.registerWorkerGauges: %w", err)
	}

	registration, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		if owed, abandoned, err := outbox.Backlog(ctx, pool); err != nil {
			log.Warn("reading outbox backlog failed", "error", err)
		} else {
			o.ObserveInt64(due, owed)
			o.ObserveInt64(exhausted, abandoned)
		}
		for _, queue := range dlqs {
			n, err := queueMessages(queue)
			if err != nil {
				log.Warn("reading dead-letter queue depth failed", "queue", queue, "error", err)
				continue
			}
			o.ObserveInt64(depth, int64(n), metric.WithAttributes(attribute.String("queue", queue)))
		}
		return nil
	}, due, exhausted, depth)
	if err != nil {
		return nil, fmt.Errorf("app.registerWorkerGauges: %w", err)
	}
	return registration, nil
}
