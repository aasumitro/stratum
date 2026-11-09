package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestQueueMessageCount(t *testing.T) {
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("RABBITMQ_URL not set")
	}
	conn, err := Dial(t.Context(), url, slog.Default())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	queue := fmt.Sprintf("test.count.%d", time.Now().UnixNano())
	// Exclusive, so the broker removes it with this test's connection.
	if _, err := ch.QueueDeclare(queue, false, false, true, false, nil); err != nil {
		t.Fatalf("declare: %v", err)
	}

	if err := ch.Confirm(false); err != nil {
		t.Fatalf("confirm mode: %v", err)
	}
	for range 2 {
		confirmation, err := ch.PublishWithDeferredConfirmWithContext(
			t.Context(), "", queue, false, false, amqp.Publishing{Body: []byte("x")})
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		if ok, err := confirmation.WaitContext(t.Context()); err != nil || !ok {
			t.Fatalf("publish not confirmed: ok=%v err=%v", ok, err)
		}
	}

	// A confirmed publish can reach the count a moment later on a busy broker.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		n, err := conn.QueueMessageCount(queue)
		if err != nil {
			t.Fatalf("QueueMessageCount: %v", err)
		}
		if n == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("count stayed %d, want 2", n)
		case <-time.After(20 * time.Millisecond):
		}
	}

	// The broker closes the channel that asked about a missing queue. The caller
	// gets an error and the connection stays usable.
	if _, err := conn.QueueMessageCount(queue + ".missing"); err == nil {
		t.Error("want an error for a missing queue")
	}
	if n, err := conn.QueueMessageCount(queue); err != nil || n != 2 {
		t.Errorf("after a failed lookup: n=%d err=%v, want 2 and no error", n, err)
	}
}
