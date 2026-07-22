package app

import (
	"database/sql"

	"github.com/aasumitro/stratum/studio/internal/connect"
)

// Services holds every Wails-bound service, constructed once at startup.
type Services struct {
	Project         *ProjectService
	Connection      *ConnectionService
	Dashboard       *DashboardService
	DLQ             *DLQService
	Reference       *ReferenceService
	Catalog         *CatalogService
	Monitor         *MonitorService
	Support         *SupportService
	Broadcast       *BroadcastService
	OrganizationOps *OrganizationOpsService
	Audit           *AuditService
	OperatorLog     *OperatorLogService
	Watchlist       *WatchlistService
}

// Wire constructs every service and wires their dependencies.
func Wire(db *sql.DB, pool *connect.PostgresPool) *Services {
	projectSvc := NewProjectService(db)
	return &Services{
		Project:         projectSvc,
		Connection:      NewConnectionService(),
		Dashboard:       NewDashboardService(projectSvc, pool),
		DLQ:             NewDLQService(projectSvc),
		Reference:       NewReferenceService(projectSvc, pool),
		Catalog:         NewCatalogService(projectSvc, pool),
		Monitor:         NewMonitorService(db, projectSvc),
		Support:         NewSupportService(db, projectSvc, pool),
		Broadcast:       NewBroadcastService(db, projectSvc, pool),
		OrganizationOps: NewOrganizationOpsService(db, projectSvc, pool),
		Audit:           NewAuditService(projectSvc, pool),
		OperatorLog:     NewOperatorLogService(db),
		Watchlist:       NewWatchlistService(projectSvc, pool),
	}
}
