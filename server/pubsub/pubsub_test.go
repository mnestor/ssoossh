package pubsub

// Test methodology: unit tests against a real (in-memory) PubSub instance
// — gochannel has no external dependency to fake, so there's no need for a
// test double. Tests run in parallel where they don't share a PubSub.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
)

// newTestPubSub builds a PubSub for tests, closing it on test cleanup.
func newTestPubSub(t *testing.T) *PubSub {
	t.Helper()

	ps, err := New(slog.Default())
	if err != nil {
		t.Fatalf("unexpected error building PubSub: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(context.Background()); err != nil {
			t.Errorf("unexpected error closing PubSub: %v", err)
		}
	})
	return ps
}

func TestNew_ShouldReturnUsablePublisherAndSubscriber(t *testing.T) {
	t.Parallel()

	ps := newTestPubSub(t)

	if ps.Publisher == nil {
		t.Fatal("expected a non-nil Publisher")
	}
	if ps.Subscriber == nil {
		t.Fatal("expected a non-nil Subscriber")
	}
	if ps.Router == nil {
		t.Fatal("expected a non-nil Router")
	}
}

func TestPubSub_ShouldDeliverPublishedMessageToSubscriber(t *testing.T) {
	t.Parallel()

	ps := newTestPubSub(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	messages, err := ps.Subscriber.Subscribe(ctx, "test-topic")
	if err != nil {
		t.Fatalf("unexpected error subscribing: %v", err)
	}

	want := []byte(`{"hello":"world"}`)
	msg := message.NewMessage(watermill.NewUUID(), want)
	if err := ps.Publisher.Publish("test-topic", msg); err != nil {
		t.Fatalf("unexpected error publishing: %v", err)
	}

	select {
	case got := <-messages:
		if string(got.Payload) != string(want) {
			t.Errorf("got payload %q, want %q", got.Payload, want)
		}
		got.Ack()
	case <-ctx.Done():
		t.Fatal("timed out waiting for the published message")
	}
}

func TestRun_ShouldReturnWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	ps := newTestPubSub(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ps.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error from Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// should drop (ack, not propagate) a message whose handler still fails after retries, and pass through a handler's success
func TestDropAfterRetries(t *testing.T) {
	t.Parallel()

	logger := watermill.NewSlogLogger(slog.Default())

	t.Run("should drop the error and return no produced messages when next fails", func(t *testing.T) {
		t.Parallel()

		next := func(msg *message.Message) ([]*message.Message, error) {
			return nil, errors.New("handler exhausted its retries")
		}
		wrapped := dropAfterRetries(logger)(next)

		produced, err := wrapped(message.NewMessage(watermill.NewUUID(), []byte("payload")))
		if err != nil {
			t.Errorf("dropAfterRetries() error = %v, want nil (dropped, not propagated)", err)
		}
		if produced != nil {
			t.Errorf("dropAfterRetries() produced = %v, want nil", produced)
		}
	})

	t.Run("should pass through a handler's success unchanged", func(t *testing.T) {
		t.Parallel()

		want := []*message.Message{message.NewMessage(watermill.NewUUID(), []byte("out"))}
		next := func(msg *message.Message) ([]*message.Message, error) {
			return want, nil
		}
		wrapped := dropAfterRetries(logger)(next)

		produced, err := wrapped(message.NewMessage(watermill.NewUUID(), []byte("payload")))
		if err != nil {
			t.Fatalf("dropAfterRetries() error = %v, want nil", err)
		}
		if len(produced) != 1 || produced[0] != want[0] {
			t.Errorf("dropAfterRetries() produced = %v, want %v", produced, want)
		}
	})
}

func TestClose_ShouldBeSafeToCallOnAnUnstartedRouter(t *testing.T) {
	t.Parallel()

	ps, err := New(slog.Default())
	if err != nil {
		t.Fatalf("unexpected error building PubSub: %v", err)
	}

	if err := ps.Close(context.Background()); err != nil {
		t.Errorf("unexpected error closing an unstarted PubSub: %v", err)
	}
}
