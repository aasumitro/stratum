// Package request handles decoding and validating incoming HTTP request
// bodies, writing a standardized 422 response on failure.
package request

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// Bind validates and decodes the request body into req, writing a 422
// validation-error response and returning false on failure. Callers should
// return immediately when this returns false.
func Bind(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
		return false
	}
	return true
}

// FieldError is one field-level validation failure. Code is the validator
// tag ("required", "min", "email", ...) and Param is that tag's argument
// where it has one (e.g. "8" for "min=8") — translated and interpolated
// frontend-side rather than rendered into English here, so the message
// isn't fixed to one language at the source.
type FieldError struct {
	Code  string `json:"code"`
	Param string `json:"param,omitempty"`
}

// ValidationError translates a ShouldBindJSON error into a field-level error
// map. Returns nil if the error is not a validation error.
//
//	if err := c.ShouldBindJSON(&req); err != nil {
//	    request.ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
//	    return
//	}
func ValidationError(err error) *response.Payload {
	ve, ok := errors.AsType[validator.ValidationErrors](err)
	if !ok {
		return response.Error("INVALID_REQUEST", err.Error())
	}

	fields := make(map[string][]FieldError, len(ve))
	for _, fe := range ve {
		field := toSnake(fe.Field())
		fields[field] = append(fields[field], FieldError{Code: fe.Tag(), Param: fe.Param()})
	}
	return response.Error("VALIDATION_FAILED", "validation failed", fields)
}

// SniffImageType reads up to the first 512 bytes of f to determine its
// actual content type via http.DetectContentType — the client-declared
// multipart Content-Type header is attacker-controlled and not trustworthy
// on its own for an image-only upload. Rewinds f back to the start before
// returning so the caller can still read the full body afterward. ok is
// false if the sniffed type isn't an image/* type.
func SniffImageType(f io.ReadSeeker) (contentType string, ok bool) {
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", false
	}
	ct := http.DetectContentType(buf[:n])
	return ct, strings.HasPrefix(ct, "image/")
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
