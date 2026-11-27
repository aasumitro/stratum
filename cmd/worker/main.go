package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	// Embed the IANA time zone database in the binary so time.LoadLocation
	// works regardless of the deploy image. The worker links the same
	// organization code path that validates timezones; keep both binaries
	// consistent so behavior does not depend on the base image.
	_ "time/tzdata"

	"github.com/aasumitro/stratum/internal/app"
	"github.com/aasumitro/stratum/internal/platform/audit"
)

// verifyArchiveCommand is the one thing this binary does besides running: it can
// share the worker's image and its database role.
const verifyArchiveCommand = "verify-audit-archive"

// Exit codes of verify-audit-archive: a scheduler must tell a finding from an outage.
const (
	exitFindings  = 1
	exitCannotRun = 2
)

func main() {
	if len(os.Args) == 1 {
		if err := app.RunWorker(); err != nil {
			slog.Error("worker stopped with error", "error", err)
			os.Exit(1)
		}
		return
	}
	if os.Args[1] != verifyArchiveCommand {
		fmt.Fprintf(os.Stderr, "usage: %s [%s [-known FILE]]\n", os.Args[0], verifyArchiveCommand)
		os.Exit(exitCannotRun)
	}
	err := verifyArchive(os.Args[2:])
	switch {
	case err == nil:
	case errors.Is(err, app.ErrArchiveNotVerified):
		os.Exit(exitFindings)
	default:
		fmt.Fprintln(os.Stderr, verifyArchiveCommand+":", err)
		os.Exit(exitCannotRun)
	}
}

func verifyArchive(args []string) error {
	flags := flag.NewFlagSet(verifyArchiveCommand, flag.ContinueOnError)
	knownFile := flags.String("known", "", "a file of event ids known to be gone, one per line")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	var known map[string]bool
	if *knownFile != "" {
		file, err := os.Open(*knownFile)
		if err != nil {
			return err
		}
		defer file.Close()
		if known, err = audit.ReadKnownIDs(file); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.VerifyAuditArchive(ctx, os.Stdout, known)
}
