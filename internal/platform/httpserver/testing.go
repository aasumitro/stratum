package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
)

// JSONTestRequest creates an HTTP request with JSON content type for use in tests.
func JSONTestRequest(method, path, body string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
