package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

// fakeAcknowledger records which of Ack/Nack/Reject was called, standing in
// for the real AMQP channel so handleDelivery can be tested without a live
// RabbitMQ connection.
type fakeAcknowledger struct {
	acked      bool
	rejected   bool
	rejectedRq bool
}

func (f *fakeAcknowledger) Ack(_ uint64, _ bool) error {
	f.acked = true
	return nil
}

func (f *fakeAcknowledger) Nack(_ uint64, _, _ bool) error { return nil }

func (f *fakeAcknowledger) Reject(_ uint64, requeue bool) error {
	f.rejected = true
	f.rejectedRq = requeue
	return nil
}

func newTestConsumer(handler Handler) (*Consumer, *fakeAcknowledger) {
	ack := &fakeAcknowledger{}
	c := &Consumer{
		spec:    ConsumerSpec{Queue: QueueSpec{Name: "test.queue"}},
		handler: handler,
		logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return c, ack
}

// TestHandleDelivery_PanicRecovered confirms a handler panic must not
// unwind past handleDelivery (which would crash the consumer goroutine, and
// with it the whole worker process), and the delivery must end up Rejected
// with requeue=true so it re-enters the same bounded-retry-then-dead-letter
// path an ordinary returned error already takes (see topology.go's
// MaxDeliveries/DLX wiring).
func TestHandleDelivery_PanicRecovered(t *testing.T) {
	c, ack := newTestConsumer(func(_ context.Context, _ []byte) error {
		panic("boom")
	})

	body, _ := json.Marshal(map[string]any{"id": "evt-1", "type": "test.event", "source": "test"})
	delivery := amqp.Delivery{Acknowledger: ack, Body: body}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("handleDelivery must recover the handler's panic, got: %v", r)
			}
		}()
		c.handleDelivery(t.Context(), delivery)
	}()

	if !ack.rejected || !ack.rejectedRq {
		t.Fatalf("expected delivery to be Reject(true)'d after a panic, got rejected=%v requeue=%v", ack.rejected, ack.rejectedRq)
	}
	if ack.acked {
		t.Fatal("a panicking handler must not result in an Ack")
	}
}

// TestHandleDelivery_ErrorRejected pins the pre-existing behavior this
// change must not disturb: a handler returning an error still Rejects with
// requeue=true, same as the panic path above.
func TestHandleDelivery_ErrorRejected(t *testing.T) {
	c, ack := newTestConsumer(func(_ context.Context, _ []byte) error {
		return errors.New("transient failure")
	})

	delivery := amqp.Delivery{Acknowledger: ack, Body: []byte(`{}`)}
	c.handleDelivery(t.Context(), delivery)

	if !ack.rejected || !ack.rejectedRq {
		t.Fatalf("expected delivery to be Reject(true)'d after an error, got rejected=%v requeue=%v", ack.rejected, ack.rejectedRq)
	}
}

// TestHandleDelivery_SuccessAcked pins the success path.
func TestHandleDelivery_SuccessAcked(t *testing.T) {
	c, ack := newTestConsumer(func(_ context.Context, _ []byte) error {
		return nil
	})

	delivery := amqp.Delivery{Acknowledger: ack, Body: []byte(`{}`)}
	c.handleDelivery(t.Context(), delivery)

	if !ack.acked {
		t.Fatal("expected a successful handler to Ack the delivery")
	}
	if ack.rejected {
		t.Fatal("a successful handler must not result in a Reject")
	}
}
