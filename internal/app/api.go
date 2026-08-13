package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

func RunAPI() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.RequireSecretsOutsideDev(); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}
	if err := cfg.RequireGeoIPDBOutsideDev(); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	infra, err := bootstrap.SetupInfra(ctx, cfg, cfg.Postgres.URL)
	if err != nil {
		return err
	}
	defer infra.Close(context.Background())

	if err := bootstrap.OpenWebhookPool(ctx, infra); err != nil {
		return err
	}

	if cfg.StatsToken == "" && cfg.Env != bootstrap.EnvDevelopment {
		infra.Log.Warn("STATS_TOKEN not set — /health/stats (goroutine/heap/pool internals) is unauthenticated")
	}

	// Declare the billing delay exchange so the API can publish delayed messages.
	// The parking queue is declared by the worker binary.
	if setupCh, chErr := infra.MQConn.Channel(); chErr == nil {
		_ = setupCh.ExchangeDeclare("billing.delay", "direct",
			true, false, false, false, nil)
		_ = setupCh.Close()
	}

	var storageClient *storage.Client
	if cfg.Storage.URL != "" {
		storageClient = storage.New(storage.Config{
			BaseURL: cfg.Storage.URL,
			Key:     cfg.Auth.ServiceRoleKey,
		})
		if err := storageClient.EnsureBuckets(ctx, []storage.BucketConfig{
			{Name: "users", Public: true, FileSizeLimit: 2 << 20},         // 2 MB — avatars
			{Name: "organization", Public: true, FileSizeLimit: 10 << 20}, // 10 MB — logos
			{Name: "platform", Public: false},                             // PDFs — no explicit limit
		}); err != nil {
			infra.Log.Warn("storage bucket init failed — uploads may fail until buckets are created", "error", err)
		}
	}

	mods, err := bootstrap.NewAPIModules(ctx, infra, storageClient)
	if err != nil {
		return err
	}

	router, err := bootstrap.NewAPIRouter(infra, mods)
	if err != nil {
		return err
	}
	defer router.AuditWriter.Stop()

	go func() {
		infra.Log.Info("starting HTTP server", "port", cfg.Port)
		if err := router.Server.Run(); err != nil {
			infra.Log.Error("HTTP server stopped", "error", err)
		}
	}()

	<-ctx.Done()
	infra.Log.Info("shutting down")
	if err := router.Server.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
