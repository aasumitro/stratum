package organization

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aasumitro/stratum/internal/platform/apperr"
)

// A refusal answers with its own code; anything else is a 500 whose cause stays
// out of the client-facing message.
func TestClassifyLeaveAndTransferErrors(t *testing.T) {
	dbErr := fmt.Errorf("organization.leaveOrganization: %w", errors.New("conn reset: password=hunter2"))
	cases := []struct {
		name       string
		got        error
		wantCode   string
		wantStatus int
	}{
		{"owner leaving", classifyLeaveError(errOwnerCannotLeave), "OWNER_CANNOT_LEAVE", http.StatusUnprocessableEntity},
		{"no membership left", classifyLeaveError(fmt.Errorf("op: %w", pgx.ErrNoRows)), "MEMBER_NOT_FOUND", http.StatusNotFound},
		{"leave internal", classifyLeaveError(dbErr), "ORGANIZATION_LEAVE_FAILED", http.StatusInternalServerError},
		{"target not a member", classifyTransferError(errTransferTargetNotMember), "TRANSFER_TARGET_NOT_MEMBER", http.StatusUnprocessableEntity},
		{"target already owner", classifyTransferError(errTransferTargetIsOwner), "TRANSFER_TARGET_ALREADY_OWNER", http.StatusUnprocessableEntity},
		{"transfer internal", classifyTransferError(dbErr), "OWNERSHIP_TRANSFER_FAILED", http.StatusInternalServerError},
		{"already classified", classifyTransferError(apperr.Forbidden("X", "x")), "X", http.StatusForbidden},
	}
	for _, tc := range cases {
		ae, ok := errors.AsType[*apperr.Error](tc.got)
		if !ok {
			t.Errorf("%s: want an *apperr.Error, got %T", tc.name, tc.got)
			continue
		}
		if ae.Code != tc.wantCode || ae.Kind.HTTPStatus() != tc.wantStatus {
			t.Errorf("%s: got %s/%d, want %s/%d", tc.name, ae.Code, ae.Kind.HTTPStatus(), tc.wantCode, tc.wantStatus)
		}
		if strings.Contains(ae.Message, "hunter2") || strings.Contains(ae.Message, "organization.") {
			t.Errorf("%s: message %q leaks the internal error", tc.name, ae.Message)
		}
	}
}

// An organization ID that is not a UUID is a missing organization, not a 500.
func TestClassifyOrganizationFetch_MalformedIDIsNotFound(t *testing.T) {
	err := classifyOrganizationFetch(fmt.Errorf("organization.findOrganizationByID: %w",
		&pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "ws-01"`}))
	ae, ok := errors.AsType[*apperr.Error](err)
	if !ok || ae.Kind != apperr.KindNotFound || !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("want a NotFound wrapping pgx.ErrNoRows, got %v", err)
	}
	if other := classifyOrganizationFetch(errors.New("connection refused")); errors.Is(other, pgx.ErrNoRows) {
		t.Errorf("a connection failure must not read as not found: %v", other)
	}
}

// A missing code is an invalid code; a failed lookup is not, so an outage never
// tells the user their code is wrong.
func TestClassifyInviteCodeLookup(t *testing.T) {
	if ae, _ := errors.AsType[*apperr.Error](classifyInviteCodeLookup(fmt.Errorf("x: %w", pgx.ErrNoRows))); ae == nil || ae.Code != "INVITE_CODE_INVALID" {
		t.Errorf("no rows: want INVITE_CODE_INVALID, got %v", ae)
	}
	// A code Postgres cannot read (invalid UTF-8, a NUL byte) is a bad code, not an outage.
	badBytes := fmt.Errorf("x: %w", &pgconn.PgError{Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\""})
	if ae, _ := errors.AsType[*apperr.Error](classifyInviteCodeLookup(badBytes)); ae == nil || ae.Code != "INVITE_CODE_INVALID" {
		t.Errorf("invalid bytes: want INVITE_CODE_INVALID, got %v", ae)
	}
	if ae, _ := errors.AsType[*apperr.Error](classifyInviteCodeLookup(errors.New("connection refused"))); ae == nil || ae.Kind != apperr.KindInternal {
		t.Errorf("connection failure: want an internal error, got %v", ae)
	}
}
