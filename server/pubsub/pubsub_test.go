package pubsub

// Test methodology: unit tests against a real (in-memory) PubSub instance
// — gochannel has no external dependency to fake, so there's no need for a
// test double. Tests run in parallel where they don't share a PubSub.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/notify"
)

// newTestPubSub builds a gochannel PubSub for tests, closing it on test cleanup.
func newTestPubSub(t *testing.T) *PubSub {
	t.Helper()

	cfg := &config.PubSubConfig{
		Backend: config.PubSubBackendGoChannel,
	}
	ps, err := New(cfg, slog.Default())
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

// should drop (ack, not propagate) a message whose handler still fails after retries, and pass through a handler's success.
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

	cfg := &config.PubSubConfig{
		Backend: config.PubSubBackendGoChannel,
	}
	ps, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error building PubSub: %v", err)
	}

	if err := ps.Close(context.Background()); err != nil {
		t.Errorf("unexpected error closing an unstarted PubSub: %v", err)
	}
}

// Queue group tests verify that topics are subscribed with the correct
// queue group semantics. These tests use SubjectCalculator directly rather
// than connecting to NATS, but confirm the derivation logic.
func TestSubjectCalculator_ShouldReturnQueueGroupForSignTopics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		topic          string
		wantQueueGroup string
	}{
		{
			name:           "certrequest.sign should have signer queue group",
			topic:          "certrequest.sign",
			wantQueueGroup: "signer",
		},
		{
			name:           "certrequest.signed should have signed-listeners queue group",
			topic:          "certrequest.signed",
			wantQueueGroup: "signed-listeners",
		},
		{
			// Without a queue group every instance would deliver the same
			// notification, and the recipient would get one copy of the
			// mail per running server.
			name:           "notification.send should have notifiers queue group",
			topic:          notify.Topic,
			wantQueueGroup: "notifiers",
		},
		{
			name:           "wait topic should have no queue group",
			topic:          "certrequest.wait.12345",
			wantQueueGroup: "",
		},
		{
			name:           "unknown topic should have no queue group",
			topic:          "unknown.topic",
			wantQueueGroup: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			detail := subjectCalculator("ssoossh", tt.topic)

			if detail.Primary != tt.topic {
				t.Errorf("got Primary %q, want %q", detail.Primary, tt.topic)
			}
			if detail.QueueGroup != tt.wantQueueGroup {
				t.Errorf("got QueueGroup %q, want %q", detail.QueueGroup, tt.wantQueueGroup)
			}
		})
	}
}

// An unset backend has to mean gochannel, not "no backend". Config files
// written before the NATS backend existed omit the field entirely, and a
// single-instance deployment is never expected to set it -- so the default
// is what most deployments actually run on.
func TestNew_ShouldDefaultToGoChannelWhenBackendIsUnset(t *testing.T) {
	t.Parallel()

	ps, err := New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("New() with an unset backend: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if ps.channel == nil {
		t.Error("expected the gochannel backend to be built for an unset backend")
	}
	if ps.natsPublisher != nil || ps.natsSubscriber != nil {
		t.Error("expected no NATS backend for an unset backend")
	}

	// Usable, not merely non-nil: the default has to produce a working pair.
	messages, err := ps.Subscriber.Subscribe(context.Background(), "default.probe")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := ps.Publisher.Publish("default.probe", message.NewMessage("1", []byte("hi"))); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case msg := <-messages:
		msg.Ack()
	case <-time.After(2 * time.Second):
		t.Fatal("published message never arrived on the default backend")
	}
}

// A NATS backend that cannot reach its broker has to fail New outright.
// The alternative -- returning a PubSub whose transport is not actually
// connected -- would let ssoosshd finish booting and then silently drop
// every sign job, which is the failure mode multi-instance deployments can
// least afford to discover in production.
//
// Uses a port nothing is listening on, so this needs no broker: it is the
// failure path that is being asserted, not the connection.
func TestNew_ShouldFailWhenTheNATSBrokerIsUnreachable(t *testing.T) {
	t.Parallel()

	// Bind and immediately release, so the port is real but closed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("closing the reserved listener: %v", err)
	}

	cfg := &config.PubSubConfig{Backend: config.PubSubBackendNATS}
	cfg.NATS.URL = "nats://" + addr

	ps, err := New(cfg, slog.Default())
	if err == nil {
		if ps != nil {
			_ = ps.Close(context.Background())
		}
		t.Fatal("New() with an unreachable broker returned no error")
	}
	if ps != nil {
		t.Errorf("New() returned a non-nil PubSub alongside an error: %+v", ps)
	}
	if !strings.Contains(err.Error(), "NATS") {
		t.Errorf("error = %v, want it to name NATS as the failing transport", err)
	}
}
