package bootstrap_test

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// goldenConsumerTopology is a frozen, field-for-field capture of every
// consumer bootstrap.NewConsumers produces, captured mechanically rather
// than transcribed by hand. One line per consumer, sorted by queue name:
// exchange|queue|bindingKeys|maxDeliveries|retryMin|retryMax|dlxRoutingKey|dlxRoutingKeyName|dlxExchange|dlxQueue|prefetch.
// Each module's own Consumers() method contributes some of these entries
// (see bootstrap.NewConsumers) — if any of them stops producing
// byte-identical topology (a dropped binding key, a renamed queue, a
// swapped DLX), this test fails with a readable diff instead of the drift
// surfacing as a silently misrouted or dead-lettered message in production.
const goldenConsumerTopology = `account.events|account.delete-account|[user.delete.request]|3|5s|1m0s|account.events.dlx|account.delete-account|account.events.dlx|account.events.dlq|5
account.events|account.export-data|[user.export.request]|3|5s|1m0s|account.events.dlx|account.export-data|account.events.dlx|account.events.dlq|5
organization.events|billing.organization-created|[organization.created]|5|2s|1m0s|billing.events.dlx|billing.organization-created|billing.events.dlx|billing.events.dlq|10
organization.events|billing.organization-deleted|[organization.deleted]|5|2s|1m0s|billing.events.dlx|billing.organization-deleted|billing.events.dlx|billing.events.dlq|10
billing.events|billing.subscription-auto-invoice|[billing.subscription.auto-invoice]|5|2s|1m0s|billing.events.dlx|billing.subscription-auto-invoice|billing.events.dlx|billing.events.dlq|10
billing.events|billing.subscription-check|[billing.subscription.check]|5|2s|1m0s|billing.events.dlx|billing.subscription-check|billing.events.dlx|billing.events.dlq|10
billing.events|billing.subscription-remind|[billing.subscription.remind]|5|2s|1m0s|billing.events.dlx|billing.subscription-remind|billing.events.dlx|billing.events.dlq|10
account.events|notification.email-changed|[user.email_changed]|5|2s|1m0s|notification.events.dlx|notification.email-changed|notification.events.dlx|notification.events.dlq|10
organization.events|notification.invitation-declined|[organization.invitation.declined]|5|2s|1m0s|notification.events.dlx|notification.invitation-declined|notification.events.dlx|notification.events.dlq|10
organization.events|notification.invitation-requested|[organization.invitation.requested]|5|2s|1m0s|notification.events.dlx|notification.invitation-requested|notification.events.dlx|notification.events.dlq|10
billing.events|notification.invoice-created|[billing.invoice.created]|5|2s|1m0s|notification.events.dlx|notification.invoice-created|notification.events.dlx|notification.events.dlq|10
billing.events|notification.invoice-failed|[billing.invoice.failed]|5|2s|1m0s|notification.events.dlx|notification.invoice-failed|notification.events.dlx|notification.events.dlq|10
billing.events|notification.invoice-paid|[billing.invoice.paid]|5|2s|1m0s|notification.events.dlx|notification.invoice-paid|notification.events.dlx|notification.events.dlq|10
organization.events|notification.member-invited|[organization.member.invited]|5|2s|1m0s|notification.events.dlx|notification.member-invited|notification.events.dlx|notification.events.dlq|10
organization.events|notification.member-removed|[organization.member.removed]|5|2s|1m0s|notification.events.dlx|notification.member-removed|notification.events.dlx|notification.events.dlq|10
organization.events|notification.member-role-changed|[organization.member.role-changed]|5|2s|1m0s|notification.events.dlx|notification.member-role-changed|notification.events.dlx|notification.events.dlq|10
organization.events|notification.organization-created|[organization.created]|5|2s|1m0s|notification.events.dlx|notification.organization-created|notification.events.dlx|notification.events.dlq|10
organization.events|notification.organization-deleted|[organization.deleted]|5|2s|1m0s|notification.events.dlx|notification.organization-deleted|notification.events.dlx|notification.events.dlq|10
organization.events|notification.organization-reactivated|[organization.reactivated]|5|2s|1m0s|notification.events.dlx|notification.organization-reactivated|notification.events.dlx|notification.events.dlq|10
organization.events|notification.organization-suspended|[organization.suspended]|5|2s|1m0s|notification.events.dlx|notification.organization-suspended|notification.events.dlx|notification.events.dlq|10
organization.events|notification.ownership-transferred|[organization.ownership.transferred]|5|2s|1m0s|notification.events.dlx|notification.ownership-transferred|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-activated|[billing.subscription.activated]|5|2s|1m0s|notification.events.dlx|notification.subscription-activated|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-cancelled|[billing.subscription.cancelled]|5|2s|1m0s|notification.events.dlx|notification.subscription-cancelled|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-expired|[billing.subscription.expired]|5|2s|1m0s|notification.events.dlx|notification.subscription-expired|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-payment-final|[billing.subscription.payment-final]|5|2s|1m0s|notification.events.dlx|notification.subscription-payment-final|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-payment-remind|[billing.subscription.payment-remind]|5|2s|1m0s|notification.events.dlx|notification.subscription-payment-remind|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-remind|[billing.subscription.remind]|5|2s|1m0s|notification.events.dlx|notification.subscription-remind|notification.events.dlx|notification.events.dlq|10
billing.events|notification.subscription-resumed|[billing.subscription.resumed]|5|2s|1m0s|notification.events.dlx|notification.subscription-resumed|notification.events.dlx|notification.events.dlq|10
billing.events|notification.trial-started|[billing.subscription.trial-started]|5|2s|1m0s|notification.events.dlx|notification.trial-started|notification.events.dlx|notification.events.dlq|10
billing.events|notification.usage-limit-warning|[billing.usage.limit-warning]|5|2s|1m0s|notification.events.dlx|notification.usage-limit-warning|notification.events.dlx|notification.events.dlq|10
organization.events|notification.webhook-auto-disabled|[organization.webhook.auto-disabled]|5|2s|1m0s|notification.events.dlx|notification.webhook-auto-disabled|notification.events.dlx|notification.events.dlq|10
organization.events|notification.webhook-health-warning|[organization.webhook.health-warning]|5|2s|1m0s|notification.events.dlx|notification.webhook-health-warning|notification.events.dlx|notification.events.dlq|10
organization.events|organization.organization-deleted|[organization.deleted]|5|2s|1m0s|organization.events.dlx|organization.organization-deleted|organization.events.dlx|organization.events.dlq|10
billing.events|organization.webhook-billing|[billing.invoice.created billing.invoice.paid billing.invoice.failed billing.subscription.activated billing.subscription.cancelled billing.subscription.expired billing.subscription.resumed]|3|5s|1m0s|organization.events.dlx|organization.webhook-billing|organization.events.dlx|organization.events.dlq|10
organization.events|organization.webhook-organization|[organization.created organization.member.invited]|3|5s|1m0s|organization.events.dlx|organization.webhook-organization|organization.events.dlx|organization.events.dlq|10
organization.events|organization.webhook-retry|[organization.webhook.retry-requested]|3|5s|1m0s|organization.events.dlx|organization.webhook-retry|organization.events.dlx|organization.events.dlq|1`

const goldenConsumerCount = 36

// consumerTopologyLines renders consumers to the same pipe-delimited format
// goldenConsumerTopology was captured in, sorted by queue name (the golden
// data's own sort key — sorting by the full line would sort by exchange
// name first instead, since that's the first field).
func consumerTopologyLines(consumers []*messaging.Consumer) []string {
	type entry struct{ queueName, line string }
	entries := make([]entry, len(consumers))
	for i, c := range consumers {
		spec := c.Spec()
		entries[i] = entry{
			queueName: spec.Queue.Name,
			line: fmt.Sprintf("%s|%s|%v|%d|%s|%s|%s|%s|%s|%s|%d",
				spec.Exchange.Name, spec.Queue.Name, spec.Queue.BindingKeys,
				spec.Queue.MaxDeliveries, spec.Queue.RetryMinDelay, spec.Queue.RetryMaxDelay,
				spec.Queue.DeadLetterExchange, spec.Queue.DeadLetterRoutingKey,
				spec.DLX.ExchangeName, spec.DLX.QueueName, spec.PrefetchCount),
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].queueName < entries[j].queueName })
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.line
	}
	return lines
}

// TestNewConsumers_MatchesGoldenTopology guards bootstrap.NewConsumers'
// full output against drift — queue name, binding keys, exchange, DLX, and
// prefetch count for all 36 entries, each contributed by one module's own
// Consumers() method. A dropped or altered entry fails this test with a
// line-level diff instead of surfacing as a silently misrouted message.
func TestNewConsumers_MatchesGoldenTopology(t *testing.T) {
	mods := bootstrap.NewWorkerModules(&bootstrap.Infra{Cfg: &config.Config{}}, nil)
	mqConn := &messaging.Connection{} // NewConsumer only stores this, never dials it
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	consumers := bootstrap.NewConsumers(mqConn, mods, log)
	if len(consumers) != goldenConsumerCount {
		t.Fatalf("want %d consumers, got %d", goldenConsumerCount, len(consumers))
	}

	got := strings.Join(consumerTopologyLines(consumers), "\n")
	if got != goldenConsumerTopology {
		t.Errorf("consumer topology drifted from the golden snapshot:\n--- got ---\n%s\n--- want ---\n%s", got, goldenConsumerTopology)
	}
}
