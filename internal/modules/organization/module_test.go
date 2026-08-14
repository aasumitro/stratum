package organization_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/organization"
)

// stub{X} embed the target interface unimplemented — MustBeWired only
// checks these fields are non-nil, it never calls a method on them, so
// there's nothing to implement.
type stubBillingReader struct{ contracts.BillingReader }
type stubBillingWriter struct{ contracts.BillingWriter }

func newUnwiredModule() *organization.Module {
	return organization.New(nil, nil, "", "", 1)
}

func TestMustBeWired_PanicsWhenAnyDependencyUnwired(t *testing.T) {
	cases := []struct {
		name string
		wire func(*organization.Module)
	}{
		{"billing reader", func(m *organization.Module) {
			m.SetBillingWriter(stubBillingWriter{})
		}},
		{"billing writer", func(m *organization.Module) {
			m.SetBillingReader(stubBillingReader{})
		}},
	}

	for _, c := range cases {
		t.Run(c.name+" unwired", func(t *testing.T) {
			m := newUnwiredModule()
			c.wire(m)
			defer func() {
				if recover() == nil {
					t.Fatalf("expected MustBeWired to panic with %s unwired", c.name)
				}
			}()
			m.MustBeWired()
		})
	}
}

func TestMustBeWired_NoPanicWhenFullyWired(t *testing.T) {
	m := newUnwiredModule()
	m.SetBillingReader(stubBillingReader{})
	m.SetBillingWriter(stubBillingWriter{})
	m.MustBeWired() // must not panic
}
