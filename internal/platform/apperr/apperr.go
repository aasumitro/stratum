// Package apperr provides typed domain errors services return to signal how
// the API layer should translate them into an HTTP response, so the
// error-to-status/code/message mapping lives once (in the service that knows
// the invariant) instead of being re-derived per handler.
package apperr

import "net/http"

type Kind int

const (
	KindInternal Kind = iota
	KindBadRequest
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindPaymentRequired
	KindValidation
)

// HTTPStatus returns the status code a Kind maps to.
func (k Kind) HTTPStatus() int {
	switch k {
	case KindBadRequest:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindPaymentRequired:
		return http.StatusPaymentRequired
	case KindValidation:
		return http.StatusUnprocessableEntity
	case KindInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// Error is a typed domain error carrying the exact response shape
// (code/message/details) a service wants returned, decoupled from the raw
// error a handler used to inspect with errors.Is.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details any
	cause   error
}

func (e *Error) Error() string { return e.Message }

// Unwrap preserves errors.Is/As against the original cause (e.g. pgx.ErrNoRows)
// for any caller other than the HTTP handler that still inspects it.
func (e *Error) Unwrap() error { return e.cause }

func newErr(kind Kind, code, message string, cause error, details []any) *Error {
	e := &Error{Kind: kind, Code: code, Message: message, cause: cause}
	if len(details) == 1 {
		e.Details = details[0]
	} else if len(details) > 1 {
		e.Details = details
	}
	return e
}

// Internal wraps an unexpected/unclassified error, preserving it as the cause
// for errors.Is/As while presenting a fixed code/message to the client.
func Internal(code, message string, cause error) *Error {
	return newErr(KindInternal, code, message, cause, nil)
}

func BadRequest(code, message string) *Error {
	return newErr(KindBadRequest, code, message, nil, nil)
}

func Unauthorized(code, message string) *Error {
	return newErr(KindUnauthorized, code, message, nil, nil)
}

func Forbidden(code, message string) *Error {
	return newErr(KindForbidden, code, message, nil, nil)
}

// NotFound wraps the cause (typically pgx.ErrNoRows) so errors.Is still
// resolves for any non-HTTP caller.
func NotFound(code, message string, cause error) *Error {
	return newErr(KindNotFound, code, message, cause, nil)
}

func Conflict(code, message string) *Error {
	return newErr(KindConflict, code, message, nil, nil)
}

func PaymentRequired(code, message string) *Error {
	return newErr(KindPaymentRequired, code, message, nil, nil)
}

func Validation(code, message string, details ...any) *Error {
	return newErr(KindValidation, code, message, nil, details)
}
