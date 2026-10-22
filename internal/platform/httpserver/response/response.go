package response

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/apperr"
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
		Error(ae.Code, ae.Message, detailsOf(ae)...).JSON(c, ae.Kind.HTTPStatus())
		return
	}
	Error("INTERNAL_ERROR", "an unexpected error occurred").JSON(c, http.StatusInternalServerError)
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
