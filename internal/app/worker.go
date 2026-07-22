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

	bootstrap.DeclareWorkerDelayQueues(infra.MQConn)

	mods := bootstrap.NewWorkerModules(infra)
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
