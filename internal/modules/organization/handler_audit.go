package organization

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// auditLogFilter parses the shared actor/action/resource/from/to query
// params (the UI's chip filters) used by both the list and export routes.
func auditLogFilter(c *gin.Context) audit.Filter {
	f := audit.Filter{
		Actor:    c.Query("actor"),
		Action:   c.Query("action"),
		Resource: c.Query("resource"),
	}
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.From = &t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.To = &t
		}
	}
	return f
}

// auditLog godoc
// @Summary      List organization audit log entries
// @Description  Page-based (limit/page) unless a cursor is supplied, in which case it switches to cursor pagination. Filterable by actor/action/resource/from/to. Admin/owner only.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true   "Organization ID"
// @Param        limit           query     int     false  "max results (default 20, max 100)"
// @Param        page            query     int     false  "page number, ignored once cursor is set (default 1)"
// @Param        cursor          query     string  false  "pagination cursor from a previous response; switches to cursor mode"
// @Param        actor           query     string  false  "filter by actor"
// @Param        action          query     string  false  "filter by action"
// @Param        resource        query     string  false  "filter by resource"
// @Param        from            query     string  false  "RFC3339 lower bound on created_at"
// @Param        to              query     string  false  "RFC3339 upper bound on created_at"
// @Success      200             {object}  response.Payload{data=[]audit.EventRecord}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/audit-log [get]
func (h *handler) auditLog(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	limit := 20
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	filter := auditLogFilter(c)

	if cursor, hasCursor := c.GetQuery("cursor"); hasCursor {
		events, nextCursor, err := audit.ListByOrganizationCursor(c.Request.Context(), h.pool, ws.ID, limit, cursor, filter)
		if err != nil {
			response.Error("AUDIT_LOG_FETCH_FAILED", "failed to list audit log").JSON(c, http.StatusInternalServerError)
			return
		}
		response.CursorList(events, nextCursor).JSON(c, http.StatusOK)
		return
	}

	page := 1
	if v, err := strconv.Atoi(c.Query("page")); err == nil && v > 0 {
		page = v
	}
	offset := (page - 1) * limit

	events, total, err := audit.ListByOrganization(c.Request.Context(), h.pool, ws.ID, limit, offset, filter)
	if err != nil {
		response.Error("AUDIT_LOG_FETCH_FAILED", "failed to list audit log").JSON(c, http.StatusInternalServerError)
		return
	}
	response.List(events, total).JSON(c, http.StatusOK)
}

// exportAuditLog godoc
// @Summary      Export organization audit log as CSV
// @Description  Streams the organization's audit log as a CSV attachment, filterable by actor/action/resource/from/to. Admin/owner only.
// @Tags         organization
// @Produce      text/csv
// @Security     BearerAuth
// @Param        organizationID  path   string  true   "Organization ID"
// @Param        actor           query  string  false  "filter by actor"
// @Param        action          query  string  false  "filter by action"
// @Param        resource        query  string  false  "filter by resource"
// @Param        from            query  string  false  "RFC3339 lower bound on created_at"
// @Param        to              query  string  false  "RFC3339 upper bound on created_at"
// @Success      200             {file}  file
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/audit-log/export [get]
func (h *handler) exportAuditLog(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	filter := auditLogFilter(c)

	c.Set("audit.action", "audit_log.export")
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=audit-log.csv")

	if err := audit.ExportByOrganization(c.Request.Context(), h.pool, ws.ID, filter, c.Writer); err != nil {
		c.Status(http.StatusInternalServerError)
	}
}
