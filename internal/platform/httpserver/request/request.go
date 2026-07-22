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

const validationTagRequired = "required"

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

// ValidationError translates a ShouldBindJSON error into a human-readable
// field-level error map. Returns nil if the error is not a validation error.
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

	fields := make(map[string][]string, len(ve))
	for _, fe := range ve {
		field := toSnake(fe.Field())
		fields[field] = append(fields[field], message(fe))
	}
	return response.Error("VALIDATION_FAILED", "validation failed", fields)
}

func message(fe validator.FieldError) string {
	field := toSnake(fe.Field())
	switch fe.Tag() {
	case validationTagRequired:
		return "The " + field + " field is required."
	case "email":
		return "The " + field + " field must be a valid email address."
	case "min":
		return "The " + field + " field must be at least " + fe.Param() + "."
	case "max":
		return "The " + field + " field must not be greater than " + fe.Param() + "."
	case "oneof":
		return "The selected " + field + " is invalid. Must be one of: " + fe.Param() + "."
	case "url":
		return "The " + field + " field must be a valid URL."
	case "uuid":
		return "The " + field + " field must be a valid UUID."
	case "len":
		return "The " + field + " field must be exactly " + fe.Param() + " characters."
	case "gt":
		return "The " + field + " field must be greater than " + fe.Param() + "."
	case "gte":
		return "The " + field + " field must be at least " + fe.Param() + "."
	case "lt":
		return "The " + field + " field must be less than " + fe.Param() + "."
	case "lte":
		return "The " + field + " field must not be greater than " + fe.Param() + "."
	default:
		return "The " + field + " field is invalid."
	}
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
