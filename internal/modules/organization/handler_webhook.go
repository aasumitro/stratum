package organization

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// ── Webhook endpoint handlers ──────────────────────────────────────────────

// safeEndpoint strips the secret from any webhook response outside of
// create/rotate — it is shown exactly once, never re-fetchable.
type safeEndpoint struct {
	ID                      string            `json:"id"`
	URL                     string            `json:"url"`
	Enabled                 bool              `json:"enabled"`
	SubscribedEvents        []string          `json:"subscribed_events,omitempty"`
	AutoDisabledAt          *time.Time        `json:"auto_disabled_at,omitempty"`
	SecretRotationExpiresAt *time.Time        `json:"secret_rotation_expires_at,omitempty"`
	Health                  *webhookHealthDTO `json:"health,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type webhookHealthDTO struct {
	SuccessPercent24h int   `json:"success_percent_24h"`
	Delivered24h      int64 `json:"delivered_24h"`
	Total24h          int64 `json:"total_24h"`
}

func toSafeEndpoint(r webhookEndpointRecord, h *webhookHealth) safeEndpoint {
	out := safeEndpoint{
		ID: r.ID, URL: r.URL, Enabled: r.Enabled, SubscribedEvents: r.SubscribedEvents,
		AutoDisabledAt: r.AutoDisabledAt, SecretRotationExpiresAt: r.SecretRotationExpiresAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if h != nil {
		percent := 100
		if h.Total24h > 0 {
			percent = int(h.Delivered24h * 100 / h.Total24h)
		}
		out.Health = &webhookHealthDTO{SuccessPercent24h: percent, Delivered24h: h.Delivered24h, Total24h: h.Total24h}
	}
	return out
}

type createWebhookRequest struct {
	URL              string   `json:"url" binding:"required,url"`
	SubscribedEvents []string `json:"subscribed_events"`
}

// createWebhook godoc
// @Summary      Create a webhook endpoint
// @Description  Owner only. The signing secret is returned once, in this response only — it is never re-fetchable.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                true  "Organization ID"
// @Param        body            body      createWebhookRequest  true  "Webhook fields"
// @Success      201             {object}  response.Payload{data=object{id=string,url=string,enabled=boolean,subscribed_events=[]string,secret=string,created_at=string,updated_at=string}}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks [post]
func (h *handler) createWebhook(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req createWebhookRequest
	if !request.Bind(c, &req) {
		return
	}

	rec, secret, err := h.svc.createWebhookEndpoint(c.Request.Context(), ws.ID, req.URL, req.SubscribedEvents)
	if err != nil {
		response.FromError(c, err)
		return
	}

	// Return secret only once on creation — it is not stored retrievably.
	out := toSafeEndpoint(*rec, nil)
	response.Success(gin.H{
		"id": out.ID, "url": out.URL, "enabled": out.Enabled, "subscribed_events": out.SubscribedEvents,
		"secret": secret, "created_at": out.CreatedAt, "updated_at": out.UpdatedAt,
	}).JSON(c, http.StatusCreated)
}

// listWebhooks godoc
// @Summary      List webhook endpoints
// @Description  Owner only. Includes 24h delivery health per endpoint.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]safeEndpoint}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks [get]
func (h *handler) listWebhooks(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	recs, err := h.svc.listWebhookEndpoints(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	var healthByEndpoint map[string]webhookHealth
	var healthErr error
	if len(recs) > 0 {
		ids := make([]string, len(recs))
		for i, r := range recs {
			ids[i] = r.ID
		}
		healthByEndpoint, healthErr = h.svc.getWebhookHealthBatch(c.Request.Context(), ids)
	}

	out := make([]safeEndpoint, len(recs))
	for i, r := range recs {
		if healthErr != nil {
			out[i] = toSafeEndpoint(r, nil)
			continue
		}
		// Zero value for an endpoint with no delivery rows yet — the batch
		// query's GROUP BY only emits a row per endpoint that has at least
		// one delivery, same as toSafeEndpoint's 100%-with-zero-total
		// default for a brand-new endpoint under the old per-endpoint query.
		health := healthByEndpoint[r.ID]
		out[i] = toSafeEndpoint(r, &health)
	}
	response.Success(out).JSON(c, http.StatusOK)
}

type updateWebhookRequest struct {
	URL              string   `json:"url" binding:"required,url"`
	Enabled          bool     `json:"enabled"`
	SubscribedEvents []string `json:"subscribed_events"`
}

// updateWebhook godoc
// @Summary      Update a webhook endpoint
// @Description  Owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                true  "Organization ID"
// @Param        webhookID       path      string                true  "Webhook ID"
// @Param        body            body      updateWebhookRequest  true  "Webhook fields"
// @Success      200             {object}  response.Payload{data=safeEndpoint}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID} [patch]
func (h *handler) updateWebhook(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	var req updateWebhookRequest
	if !request.Bind(c, &req) {
		return
	}

	rec, err := h.svc.updateWebhookEndpoint(c.Request.Context(), ws.ID, webhookID, req.URL, req.Enabled, req.SubscribedEvents)
	if err != nil {
		response.FromError(c, err)
		return
	}

	response.Success(toSafeEndpoint(*rec, nil)).JSON(c, http.StatusOK)
}

// rotateWebhookSecret godoc
// @Summary      Rotate a webhook's signing secret
// @Description  Owner only. The new secret is returned once, in this response only.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        webhookID       path      string  true  "Webhook ID"
// @Success      200             {object}  response.Payload{data=object{id=string,secret=string,secret_rotation_expires_at=string}}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID}/rotate-secret [post]
func (h *handler) rotateWebhookSecret(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	rec, secret, err := h.svc.rotateWebhookSecret(c.Request.Context(), ws.ID, webhookID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	out := toSafeEndpoint(*rec, nil)
	response.Success(gin.H{
		"id": out.ID, "secret": secret, "secret_rotation_expires_at": out.SecretRotationExpiresAt,
	}).JSON(c, http.StatusOK)
}

// sendWebhookTestEvent godoc
// @Summary      Send a test event
// @Description  Owner only. Fires a synthetic delivery to verify the endpoint is reachable and correctly signed.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        webhookID       path      string  true  "Webhook ID"
// @Success      200             {object}  response.Payload{data=object{success=boolean,delivery=webhookDeliveryRecord}}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID}/test-event [post]
func (h *handler) sendWebhookTestEvent(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	del, success, err := h.svc.sendTestEvent(c.Request.Context(), ws.ID, webhookID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(gin.H{"success": success, "delivery": del}).JSON(c, http.StatusOK)
}

// deleteWebhook godoc
// @Summary      Delete a webhook endpoint
// @Description  Owner only.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        webhookID       path  string  true  "Webhook ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID} [delete]
func (h *handler) deleteWebhook(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	if err := h.svc.deleteWebhookEndpoint(c.Request.Context(), ws.ID, webhookID); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// listWebhookDeliveries godoc
// @Summary      List webhook deliveries
// @Description  Cursor-paginated delivery attempts for an endpoint, optionally filtered. Owner only.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true   "Organization ID"
// @Param        webhookID       path      string  true   "Webhook ID"
// @Param        limit           query     int     false  "max results (default 20, max 100)"
// @Param        cursor          query     string  false  "pagination cursor from a previous response"
// @Param        status          query     string  false  "filter by delivery status"
// @Param        event_type      query     string  false  "filter by event type"
// @Param        since           query     string  false  "RFC3339 lower bound on created_at"
// @Success      200             {object}  response.Payload{data=[]webhookDeliveryRecord}
// @Failure      404             {object}  response.Payload  "webhook not found"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID}/deliveries [get]
func (h *handler) listWebhookDeliveries(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	// Verify endpoint belongs to organization.
	_, err := h.svc.repo.findWebhookEndpoint(c.Request.Context(), h.pool, ws.ID, webhookID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			response.Error("WEBHOOK_NOT_FOUND", "webhook not found").JSON(c, http.StatusNotFound)
			return
		}
		response.Error("WEBHOOK_FETCH_FAILED", "failed to fetch webhook").JSON(c, http.StatusInternalServerError)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	cursor := c.Query("cursor")

	filter := webhookDeliveryFilter{Status: c.Query("status"), EventType: c.Query("event_type")}
	if since := c.Query("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = &t
		}
	}

	recs, nextCursor, err := h.svc.listWebhookDeliveries(c.Request.Context(), webhookID, filter, cursor, limit)
	if err != nil {
		response.Error("DELIVERY_LIST_FAILED", "failed to list deliveries").JSON(c, http.StatusInternalServerError)
		return
	}
	response.CursorList(recs, nextCursor).JSON(c, http.StatusOK)
}

// retryWebhookDelivery godoc
// @Summary      Retry a single webhook delivery
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        webhookID       path  string  true  "Webhook ID"
// @Param        deliveryID      path  string  true  "Delivery ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID}/deliveries/{deliveryID}/retry [post]
func (h *handler) retryWebhookDelivery(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")
	deliveryID := c.Param("deliveryID")

	if err := h.svc.retryWebhookDelivery(c.Request.Context(), ws.ID, webhookID, deliveryID); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// retryAllFailedWebhookDeliveries godoc
// @Summary      Retry all failed webhook deliveries
// @Description  Queues up to the 50 most recent failed deliveries for this endpoint to be retried asynchronously — the returned count is how many were queued, not how many ultimately succeed.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        webhookID       path      string  true  "Webhook ID"
// @Success      200             {object}  response.Payload{data=object{retried=integer}}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/webhooks/{webhookID}/deliveries/retry-failed [post]
func (h *handler) retryAllFailedWebhookDeliveries(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	webhookID := c.Param("webhookID")

	count, err := h.svc.retryAllFailedWebhookDeliveries(c.Request.Context(), ws.ID, webhookID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(gin.H{"retried": count}).JSON(c, http.StatusOK)
}
