package notification

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

// Module owns in-app, push, and email notifications.
type Module struct {
	svc    *service
	Worker *Worker
}

func New(
	pool *pgxpool.Pool,
	m *mailer.Mailer,
	orgReader contracts.OrganizationReader,
	userReader contracts.UserReader,
	appURL string,
	redis *goredis.Client,
) *Module {
	svc := &service{
		repo:       &repository{},
		pool:       pool,
		mailer:     m,
		orgReader:  orgReader,
		userReader: userReader,
		appURL:     appURL,
		redis:      redis,
	}
	return &Module{svc: svc, Worker: &Worker{svc: svc}}
}

// DeleteAllForUser implements contracts.NotificationWriter — deletes every
// notification message addressed to a user. Called by the account module's
// GDPR delete-account flow.
func (m *Module) DeleteAllForUser(ctx context.Context, authSub string) error {
	return m.svc.deleteAllForUser(ctx, authSub)
}

// ListForUser implements contracts.NotificationReader. Called by the
// account module's GDPR data-export flow.
func (m *Module) ListForUser(ctx context.Context, authSub string, limit int) ([]contracts.MessageInfo, error) {
	msgs, err := m.svc.listForExport(ctx, authSub, limit)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.MessageInfo, len(msgs))
	for i, msg := range msgs {
		out[i] = contracts.MessageInfo{
			ID: msg.ID, OrganizationID: msg.OrganizationID, Kind: msg.Kind,
			Channel: msg.Channel, Subject: msg.Subject, Body: msg.Body, CreatedAt: msg.CreatedAt,
		}
	}
	return out, nil
}

// Register mounts user-level notification routes under /me/notifications (auth only).
//
//	GET   /me/notifications               ?organization_id= optional filter
//	GET   /me/notifications/stream        SSE push (requires Redis)
//	GET   /me/notifications/unread-count  ?organization_id= optional filter
//	PATCH /me/notifications/read-all      ?organization_id= optional filter
//	PATCH /me/notifications/:id/read
func (m *Module) Register(r *gin.RouterGroup, deps httpserver.RouteDeps) {
	h := &handler{svc: m.svc}

	// /stream is registered outside the group with AuthSSE (Auth plus a
	// __session cookie fallback) instead of deps.Auth — EventSource can't
	// set the Authorization header, and every other route must not accept
	// the cookie (see RouteDeps.AuthSSE).
	if m.svc.redis != nil {
		r.GET("/me/notifications/stream", deps.AuthSSE, deps.RateLimit, h.streamNotifications)
	}

	g := r.Group("/me/notifications")
	g.Use(deps.Auth, deps.RateLimit)
	{
		g.GET("", h.listNotifications)
		g.GET("/unread-count", h.unreadCount)
		g.PATCH("/read-all", h.markAllRead)
		g.PATCH("/:id/read", h.markRead)
		g.GET("/preferences", h.listPreferences)
		g.PATCH("/preferences", h.upsertPreference)
	}
}
