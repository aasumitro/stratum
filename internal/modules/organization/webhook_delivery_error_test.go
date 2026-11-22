package organization

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A webhook URL can carry a token in its path or query; a failed delivery's text
// is stored and logged, so it must never contain the URL.
func TestDeliveryError_DropsTheEndpointURL(t *testing.T) {
	const token = "s3cr3t-token"
	_, parseErr := http.NewRequest(http.MethodPost, "https://hooks.example.com/%zz/"+token, nil)
	doErr := fmt.Errorf("httpclient: %w", &url.Error{
		Op: "Post", URL: "https://hooks.example.com/services/" + token + "?key=" + token,
		Err: errors.New("dial tcp: connection refused"),
	})
	for name, err := range map[string]error{"parse": parseErr, "do": doErr} {
		got := deliveryError(err)
		if strings.Contains(got, token) || strings.Contains(got, "hooks.example.com/") {
			t.Errorf("%s: %q still carries the URL", name, got)
		}
		if got == "" {
			t.Errorf("%s: want a description, got nothing", name)
		}
	}
	if got := deliveryError(errors.New("plain")); got != "plain" {
		t.Errorf("a non-URL error: want it unchanged, got %q", got)
	}
}
