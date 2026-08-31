package main

import (
	"log/slog"
	"os"
	// Embed the IANA time zone database in the binary so time.LoadLocation
	// works regardless of the deploy image. Organization settings validation
	// rejects any timezone LoadLocation cannot resolve; on a minimal base
	// image without OS zoneinfo that would reject every valid IANA name.
	_ "time/tzdata"

	"github.com/aasumitro/stratum/internal/app"
)

func main() {
	if err := app.RunAPI(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
