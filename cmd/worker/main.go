package main

import (
	"log/slog"
	"os"
	// Embed the IANA time zone database in the binary so time.LoadLocation
	// works regardless of the deploy image. The worker links the same
	// organization code path that validates timezones; keep both binaries
	// consistent so behavior does not depend on the base image.
	_ "time/tzdata"

	"github.com/aasumitro/stratum/internal/app"
)

func main() {
	if err := app.RunWorker(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
