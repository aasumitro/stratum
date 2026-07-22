package account

import (
	"context"

	"github.com/aasumitro/stratum/internal/contracts/events"
)

// Worker processes async account tasks (GDPR delete, data export).
type Worker struct {
	svc *service
}

func (w *Worker) HandleDeleteAccount(ctx context.Context, body []byte) error {
	req, err := events.Decode[events.UserTaskRequest](body)
	if err != nil {
		return err
	}
	return w.svc.executeDeleteAccount(ctx, req.TaskID, req.AuthSub)
}

func (w *Worker) HandleExportData(ctx context.Context, body []byte) error {
	req, err := events.Decode[events.UserTaskRequest](body)
	if err != nil {
		return err
	}
	return w.svc.executeExportData(ctx, req.TaskID, req.AuthSub)
}
