package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/aasumitro/stratum/studio/app"
	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/store"
	"github.com/adrg/xdg"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dataDir := filepath.Join(xdg.DataHome, "stratum-studio")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("stratum-studio: create data dir: %v", err)
	}

	db, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("stratum-studio: open store: %v", err)
	}
	defer db.Close()

	pgPool := connect.NewPostgresPool()
	defer pgPool.CloseAll()

	svcs := app.Wire(db, pgPool)
	defer svcs.Monitor.StopAll()

	studioApp := application.New(application.Options{
		Name:        "Stratum Studio",
		Description: "Desktop operator console for Stratum SaaS projects",
		Services: []application.Service{
			application.NewService(svcs.Project),
			application.NewService(svcs.Connection),
			application.NewService(svcs.Dashboard),
			application.NewService(svcs.DLQ),
			application.NewService(svcs.Reference),
			application.NewService(svcs.Catalog),
			application.NewService(svcs.Monitor),
			application.NewService(svcs.Support),
			application.NewService(svcs.Broadcast),
			application.NewService(svcs.OrganizationOps),
			application.NewService(svcs.Audit),
			application.NewService(svcs.OperatorLog),
			application.NewService(svcs.Watchlist),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	studioApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Stratum Studio",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Windows: application.WindowsWindow{
			BackdropType: application.Mica,
			Theme:        application.SystemDefault,
		},
		Linux: application.LinuxWindow{
			WindowIsTranslucent: false,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	if err := studioApp.Run(); err != nil {
		// log.Fatalf calls os.Exit, which would skip the db.Close/pgPool.CloseAll/
		// monitorSvc.StopAll defers above — log and let main return normally instead.
		log.Printf("stratum-studio: run: %v", err)
	}
}
