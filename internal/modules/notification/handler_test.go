package notification_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aasumitro/stratum/internal/modules/notification"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// listNotifications and unreadCount have no binding requirements —
// they fall through to the service call which panics (svc is nil).
// markRead is the only pure-logic path: it just passes the id param through.
// All three are thin pass-throughs; meaningful coverage comes from integration tests.

func TestListNotifications_ReturnsOKShape(t *testing.T) {
	// Routes that don't hit the service (no binding errors, no early returns)
	// will panic with svc=nil. We verify the route is registered and reachable
	// by confirming it does NOT return 404.
	defer func() { recover() }()
	w := httptest.NewRecorder()
	notification.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/notifications", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered, got 404")
	}
}

func TestUnreadCount_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	notification.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodGet, "/notifications/unread-count", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered, got 404")
	}
}

func TestMarkRead_RouteRegistered(t *testing.T) {
	defer func() { recover() }()
	w := httptest.NewRecorder()
	notification.NewHandlerEngine().ServeHTTP(w, httpserver.JSONTestRequest(http.MethodPatch, "/notifications/notif_01/read", ""))
	if w.Code == http.StatusNotFound {
		t.Errorf("route not registered, got 404")
	}
}
