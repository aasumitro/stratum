package apperr_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/apperr"
)

func TestKind_HTTPStatus(t *testing.T) {
	cases := []struct {
		kind apperr.Kind
		want int
	}{
		{apperr.KindInternal, http.StatusInternalServerError},
		{apperr.KindBadRequest, http.StatusBadRequest},
		{apperr.KindUnauthorized, http.StatusUnauthorized},
		{apperr.KindForbidden, http.StatusForbidden},
		{apperr.KindNotFound, http.StatusNotFound},
		{apperr.KindConflict, http.StatusConflict},
		{apperr.KindPaymentRequired, http.StatusPaymentRequired},
		{apperr.KindValidation, http.StatusUnprocessableEntity},
		{apperr.Kind(9999), http.StatusInternalServerError}, // unknown kind → 500 default
	}
	for _, c := range cases {
		if got := c.kind.HTTPStatus(); got != c.want {
			t.Errorf("Kind(%d).HTTPStatus() = %d, want %d", c.kind, got, c.want)
		}
	}
}

func TestConstructors_KindAndStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        *apperr.Error
		wantKind   apperr.Kind
		wantStatus int
	}{
		{"BadRequest", apperr.BadRequest("BAD", "bad"), apperr.KindBadRequest, http.StatusBadRequest},
		{"Unauthorized", apperr.Unauthorized("UNAUTH", "no"), apperr.KindUnauthorized, http.StatusUnauthorized},
		{"Forbidden", apperr.Forbidden("FORBID", "no"), apperr.KindForbidden, http.StatusForbidden},
		{"Conflict", apperr.Conflict("CONFLICT", "dup"), apperr.KindConflict, http.StatusConflict},
		{"PaymentRequired", apperr.PaymentRequired("PAY", "pay"), apperr.KindPaymentRequired, http.StatusPaymentRequired},
		{"Validation", apperr.Validation("VAL", "invalid"), apperr.KindValidation, http.StatusUnprocessableEntity},
		{"Internal", apperr.Internal("INT", "boom", nil), apperr.KindInternal, http.StatusInternalServerError},
		{"NotFound", apperr.NotFound("NF", "missing", nil), apperr.KindNotFound, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err.Kind != c.wantKind {
				t.Errorf("Kind = %d, want %d", c.err.Kind, c.wantKind)
			}
			if c.err.Kind.HTTPStatus() != c.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d", c.err.Kind.HTTPStatus(), c.wantStatus)
			}
			if c.err.Code == "" || c.err.Message == "" {
				t.Errorf("code/message must be populated: %+v", c.err)
			}
		})
	}
}

func TestError_MessageIsErrorString(t *testing.T) {
	e := apperr.BadRequest("X", "the message")
	if e.Error() != "the message" {
		t.Errorf("Error() = %q, want %q", e.Error(), "the message")
	}
}

func TestError_UnwrapPreservesCause(t *testing.T) {
	sentinel := errors.New("root cause")

	nf := apperr.NotFound("NF", "not found", sentinel)
	if !errors.Is(nf, sentinel) {
		t.Error("NotFound must preserve its cause for errors.Is")
	}

	in := apperr.Internal("INT", "wrapped", fmt.Errorf("layer: %w", sentinel))
	if !errors.Is(in, sentinel) {
		t.Error("Internal must preserve a wrapped cause through Unwrap")
	}

	// constructors without a cause unwrap to nil (no false positive match)
	if errors.Is(apperr.Conflict("C", "c"), sentinel) {
		t.Error("Conflict has no cause; must not match an unrelated sentinel")
	}
}

func TestValidation_DetailsShape(t *testing.T) {
	// no details → nil
	if d := apperr.Validation("V", "m").Details; d != nil {
		t.Errorf("no details should be nil, got %v", d)
	}
	// exactly one detail → unwrapped single value
	one := apperr.Validation("V", "m", "field required").Details
	if one != "field required" {
		t.Errorf("single detail should be unwrapped, got %v", one)
	}
	// multiple details → slice
	many := apperr.Validation("V", "m", "a", "b").Details
	slice, ok := many.([]any)
	if !ok || len(slice) != 2 {
		t.Errorf("multiple details should be a 2-element slice, got %#v", many)
	}
}

// apperr.Error must satisfy the error interface and be usable with errors.As.
func TestError_AsTarget(t *testing.T) {
	var target *apperr.Error
	err := error(apperr.Forbidden("F", "denied"))
	if !errors.As(err, &target) {
		t.Fatal("errors.As should extract *apperr.Error")
	}
	if target.Code != "F" {
		t.Errorf("extracted wrong error: %+v", target)
	}
}
