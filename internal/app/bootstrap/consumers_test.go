package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// TestDeclareWorkerDelayQueues_NoConnection_ReturnsWrappedError covers the
// error-propagation path: a declare failure must return a wrapped error,
// not be silently swallowed. A zero-value *messaging.Connection.Channel()
// fails immediately without touching the network ("connection is not
// currently open" — see messaging.Connection.Channel), which is enough to
// exercise this without live RabbitMQ.
func TestDeclareWorkerDelayQueues_NoConnection_ReturnsWrappedError(t *testing.T) {
	err := bootstrap.DeclareWorkerDelayQueues(&messaging.Connection{})
	if err == nil {
		t.Fatal("want a wrapped error when the underlying connection has no open channel, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap.DeclareWorkerDelayQueues") {
		t.Errorf("want error wrapped with %q context, got %v", "bootstrap.DeclareWorkerDelayQueues", err)
	}
}
