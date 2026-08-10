package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

func RunWorker() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	infra, err := bootstrap.SetupInfra(ctx, cfg)
	if err != nil {
		return err
	}
	defer infra.Close(context.Background())

	if err := bootstrap.DeclareWorkerDelayQueues(infra.MQConn); err != nil {
		return fmt.Errorf("declaring worker delay topology: %w", err)
	}
	infra.Log.Info("worker delay topology declared")

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
	wg.Go(func() { bootstrap.RunDelayTopologyReconnectLoop(ctx, infra.MQConn, infra.Log) })

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

	infra.Log.Info("worker running", "consumers", len(consumers))
	<-ctx.Done()
	infra.Log.Info("worker shutting down, waiting for in-flight deliveries to finish")
	wg.Wait()
	infra.Log.Info("worker shutdown complete")
	return nil
}
