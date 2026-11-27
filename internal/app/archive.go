package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/otel"
)

// ErrArchiveNotVerified means a verification ran, and its report says what is wrong.
// Any other error means it could not run.
var ErrArchiveNotVerified = errors.New("the audit archive did not verify")

func archiveFor(cfg config.AuditArchiveConfig, tableDays int, pool *pgxpool.Pool, log *slog.Logger) *audit.Archive {
	return audit.NewArchive(pool, audit.ArchiveStoreConfig{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
	}, log, cfg.RetentionDays, tableDays)
}

// newAuditArchive returns the archive the worker exports to, or nil when none is
// configured. A bucket that answers it has no Object Lock stops the boot: it would
// hold the copy without protecting it. A bucket that cannot be asked does not, since
// the worker has other work, and every run asks again.
func newAuditArchive(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) (*audit.Archive, error) {
	if err := cfg.ValidateArchive(); err != nil {
		return nil, err
	}
	if !cfg.Archive.Enabled() {
		if cfg.Env != bootstrap.EnvDevelopment {
			log.Warn("audit events are not copied to write-once storage", "env", "AUDIT_ARCHIVE_ENDPOINT")
		}
		return nil, nil
	}
	archive := archiveFor(cfg.Archive, cfg.AuditRetentionDays, pool, log)
	// A bucket that does not answer must not hold the boot up for long.
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := archive.CheckLock(checkCtx)
	if errors.Is(err, audit.ErrArchiveNotLocked) {
		return nil, err
	}
	if err != nil {
		log.Warn("audit archive could not be checked, retrying on the next run", "error", err)
	}
	return archive, nil
}

// registerArchiveGauge reports the audit rows that should have been copied by now.
func registerArchiveGauge(archive *audit.Archive, log *slog.Logger) (metric.Registration, error) {
	meter := otel.Meter()
	pending, err := meter.Int64ObservableGauge("stratum.audit.archive.pending",
		metric.WithDescription("Audit rows older than two hours that have not reached the archive."))
	if err != nil {
		return nil, fmt.Errorf("app.registerArchiveGauge: %w", err)
	}
	registration, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		rows, err := archive.Pending(ctx)
		if err != nil {
			log.Warn("counting unarchived audit rows failed", "error", err)
			return nil
		}
		o.ObserveInt64(pending, rows)
		return nil
	}, pending)
	if err != nil {
		return nil, fmt.Errorf("app.registerArchiveGauge: %w", err)
	}
	return registration, nil
}

// VerifyAuditArchive compares the audit table with its archive and prints what it
// found. It reads only its own settings and opens only the database and the bucket,
// so it can run on a host that holds nothing else. known lists the ids of rows known
// to be gone, such as those an older backup lost.
func VerifyAuditArchive(ctx context.Context, out io.Writer, known map[string]bool) error {
	cfg, err := config.LoadArchiveVerify()
	if err != nil {
		return err
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("POSTGRES_WORKER_URL is not a valid connection string")
	}
	// It reads the whole table, which is not the runaway query a timeout is for. Every
	// name it uses is schema-qualified, and built-ins come from pg_catalog alone: the
	// database is what is checked, so it must not supply a function of its own.
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = "0"
	poolCfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	poolCfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("setting up postgres: %w", err)
	}
	defer pool.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	report, err := archiveFor(cfg.Archive, cfg.AuditRetentionDays, pool, log).Verify(ctx, known)
	if err != nil {
		return err
	}
	report.Write(out)
	if !report.OK() {
		return ErrArchiveNotVerified
	}
	return nil
}
