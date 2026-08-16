package account_test

import (
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/modules/account"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// stub{X} embed the target interface unimplemented — MustBeWired only
// checks these fields are non-nil, it never calls a method on them, so
// there's nothing to implement.
type stubOrgWriter struct{ contracts.OrganizationWriter }
type stubNotifWriter struct{ contracts.NotificationWriter }
type stubBillingWriter struct{ contracts.BillingWriter }
type stubOrgReader struct{ contracts.OrganizationReader }
type stubNotifReader struct{ contracts.NotificationReader }

func newUnwiredModule() *account.Module {
	return account.New(nil, messaging.NoopPublisher{}, "", "", nil, nil, "", time.Hour)
}

func TestMustBeWired_PanicsWhenAnyDependencyUnwired(t *testing.T) {
	cases := []struct {
		name string
		wire func(*account.Module)
	}{
		{"organization writer", func(m *account.Module) {
			m.SetNotificationWriter(stubNotifWriter{})
			m.SetBillingWriter(stubBillingWriter{})
			m.SetOrganizationReader(stubOrgReader{})
			m.SetNotificationReader(stubNotifReader{})
		}},
		{"notification writer", func(m *account.Module) {
			m.SetOrganizationWriter(stubOrgWriter{})
			m.SetBillingWriter(stubBillingWriter{})
			m.SetOrganizationReader(stubOrgReader{})
			m.SetNotificationReader(stubNotifReader{})
		}},
		{"billing writer", func(m *account.Module) {
			m.SetOrganizationWriter(stubOrgWriter{})
			m.SetNotificationWriter(stubNotifWriter{})
			m.SetOrganizationReader(stubOrgReader{})
			m.SetNotificationReader(stubNotifReader{})
		}},
		{"organization reader", func(m *account.Module) {
			m.SetOrganizationWriter(stubOrgWriter{})
			m.SetNotificationWriter(stubNotifWriter{})
			m.SetBillingWriter(stubBillingWriter{})
			m.SetNotificationReader(stubNotifReader{})
		}},
		{"notification reader", func(m *account.Module) {
			m.SetOrganizationWriter(stubOrgWriter{})
			m.SetNotificationWriter(stubNotifWriter{})
			m.SetBillingWriter(stubBillingWriter{})
			m.SetOrganizationReader(stubOrgReader{})
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
	m.SetOrganizationWriter(stubOrgWriter{})
	m.SetNotificationWriter(stubNotifWriter{})
	m.SetBillingWriter(stubBillingWriter{})
	m.SetOrganizationReader(stubOrgReader{})
	m.SetNotificationReader(stubNotifReader{})
	m.MustBeWired() // must not panic
}

func TestMustHaveSessionRevocationWired_PanicsWhenUnwired(t *testing.T) {
	m := account.New(nil, messaging.NoopPublisher{}, "", "", nil, nil, "", time.Hour)
	defer func() {
		if recover() == nil {
			t.Fatal("expected MustHaveSessionRevocationWired to panic with revokedNS unwired")
		}
	}()
	m.MustHaveSessionRevocationWired()
}

func TestMustHaveSessionRevocationWired_NoPanicWhenWired(t *testing.T) {
	revokedNS := cache.NewNamespace(nil, "account")
	m := account.New(nil, messaging.NoopPublisher{}, "", "", revokedNS, nil, "", time.Hour)
	m.MustHaveSessionRevocationWired() // must not panic
}
