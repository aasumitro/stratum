package response

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/logger"
)

const requestIDHeader = "X-Request-ID"

type Payload struct {
	TotalData  int64       `json:"-"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Data       any         `json:"data,omitempty"`
	Status     *Status     `json:"status,omitempty"`
	Pagination *pagination `json:"pagination,omitempty"`
}

type Status struct {
	RequestID string `json:"request_id"`
	Error     bool   `json:"error"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Details   any    `json:"details,omitempty"`
}

type pagination struct {
	Limit       int   `json:"limit"`
	Offset      int   `json:"offset"`
	CurrentPage int   `json:"current_page"`
	TotalPages  int   `json:"total_pages"`
	TotalItems  int64 `json:"total_items"`
}

func (p *Payload) JSON(c *gin.Context, code int) {
	reqID := c.Writer.Header().Get(requestIDHeader)
	isError := code >= http.StatusBadRequest
	if p.Status != nil {
		p.Status.RequestID = reqID
		p.Status.Error = isError
	} else {
		p.Status = &Status{RequestID: reqID, Error: isError}
	}
	p.paginate(c)
	c.JSON(code, p)
}

func (p *Payload) paginate(c *gin.Context) {
	if p.NextCursor != "" {
		return // cursor mode — skip page-based pagination metadata
	}
	limit, offset, page := paginationParams(c)
	if p.Data == nil || p.TotalData <= int64(limit) {
		return
	}
	p.Pagination = &pagination{
		Limit:       limit,
		Offset:      offset,
		CurrentPage: page,
		TotalItems:  p.TotalData,
		TotalPages:  int(math.Ceil(float64(p.TotalData) / float64(limit))),
	}
}

// paginationParams reads limit/offset/page from query strings with sane defaults.
func paginationParams(c *gin.Context) (limit, offset, page int) {
	limit = 20
	if v := c.GetInt("limit"); v > 0 {
		limit = v
	} else if q := c.Query("limit"); q != "" {
		if n := parseInt(q, 20); n > 0 {
			limit = n
		}
	}

	page = 1
	if q := c.Query("page"); q != "" {
		if n := parseInt(q, 1); n > 0 {
			page = n
		}
	}

	offset = (page - 1) * limit
	if q := c.Query("offset"); q != "" {
		if n := parseInt(q, 0); n >= 0 {
			offset = n
		}
	}

	return limit, offset, page
}

func parseInt(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}

// Error builds a standardized error payload with a machine-readable code.
func Error(code, message string, details ...any) *Payload {
	s := &Status{Error: true, Code: code, Message: message}
	if len(details) == 1 {
		s.Details = details[0]
	} else if len(details) > 1 {
		s.Details = details
	}
	return &Payload{Status: s}
}

// FromError translates a service-returned error into the standard error
// payload and status code. A *apperr.Error carries its own code/message/kind;
// anything else is treated as an unclassified internal error.
func FromError(c *gin.Context, err error) {
	if ae, ok := errors.AsType[*apperr.Error](err); ok {
		status := ae.Kind.HTTPStatus()
		if status >= http.StatusInternalServerError {
			LogInternal(c, ae.Code, err)
		}
		Error(ae.Code, ae.Message, detailsOf(ae)...).JSON(c, status)
		return
	}
	LogInternal(c, "INTERNAL_ERROR", err)
	Error("INTERNAL_ERROR", "an unexpected error occurred").JSON(c, http.StatusInternalServerError)
}

// LogInternal records the error behind a 5xx response, once, on the
// request-scoped logger (which carries request_id and trace IDs) and on the
// request span. The client gets only a code and a fixed message, so this is the
// one record of what failed: the error and, for an *apperr.Error, the cause its
// Error() leaves out. Every 5xx writer calls it, through FromError or directly
// when it writes its own body. A request the client abandoned is logged as a
// warning, since its context.Canceled is not a server fault. Readiness probes
// report their own dependency state and a recovered panic logs itself, so
// neither comes through here.
func LogInternal(c *gin.Context, code string, err error) {
	ctx := c.Request.Context()
	attrs := []any{"code", code, "method", c.Request.Method, "route", c.FullPath(), "error", err}
	recorded := err
	if cause := apperr.Cause(err); cause != nil {
		attrs = append(attrs, "cause", cause)
		recorded = cause
		// The cause an *apperr.Error wraps directly can carry context (an ID) that
		// a nested *apperr.Error between it and the innermost cause hides.
		if ae, ok := errors.AsType[*apperr.Error](err); ok && ae.Unwrap() != cause {
			attrs = append(attrs, "wrapped", ae.Unwrap())
		}
	}
	log := logger.FromContext(ctx)
	// Only a request the client abandoned is benign; a cancel from inside the
	// server, with the request still live, is a fault like any other.
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		log.WarnContext(ctx, "request cancelled", attrs...)
		return
	}
	log.ErrorContext(ctx, "request failed", attrs...)
	trace.SpanFromContext(ctx).RecordError(recorded, trace.WithAttributes(attribute.String("error.code", code)))
}

func detailsOf(ae *apperr.Error) []any {
	if ae.Details == nil {
		return nil
	}
	return []any{ae.Details}
}

// Success builds a data payload.
func Success(data any) *Payload {
	return &Payload{Data: data}
}

// List builds a paginated data payload. Set TotalData before calling .JSON.
func List(data any, total int64) *Payload {
	return &Payload{Data: data, TotalData: total}
}

// CursorList builds a cursor-paginated payload. nextCursor is empty when there are no more pages.
func CursorList(data any, nextCursor string) *Payload {
	return &Payload{Data: data, NextCursor: nextCursor}
}
