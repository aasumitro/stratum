package main

import (
	"log/slog"
	"os"

	"github.com/aasumitro/stratum/internal/app"
)

func main() {
	if err := app.RunAPI(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
