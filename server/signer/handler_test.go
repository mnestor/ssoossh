package signer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"

	"github.com/mnestor/ssoossh/server/certmsg"
)

// newDefaultTestHandlerLimits returns SignLimits with generous defaults suitable
// for handler tests.
func newDefaultTestHandlerLimits() SignLimits {
	return SignLimits{
		MaxCertLifetime:     time.Hour * 24 * 90,
		MaxHostCertLifetime: time.Hour * 24 * 365 * 2,
	}
}

// Test methodology: the handler is exercised directly (rather than through a
// running Router) so each case asserts one thing — what got published, and
// whether the message was acked — without needing router lifecycle
// scaffolding. The end-to-end wiring is covered by the pipeline test in
// server/service.

// newTestChannel returns a non-persistent gochannel pair, matching
// server/pubsub.New, closed on cleanup.
func newTestChannel(t *testing.T) *gochannel.GoChannel {
	t.Helper()

	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})
	return channel
}

// handleJob runs h.handle against job and returns the reply published to
// SignedTopic, plus the handler's own error.
func handleJob(t *testing.T, h *Handler, channel *gochannel.GoChannel, job certmsg.SigningJob) (certmsg.SignedReply, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	replies, err := channel.Subscribe(ctx, certmsg.SignedTopic)
	if err != nil {
		t.Fatalf("failed to subscribe to replies: %v", err)
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to encode job: %v", err)
	}

	handleErr := h.handle(message.NewMessage(watermill.NewUUID(), payload))

	select {
	case msg := <-replies:
		var reply certmsg.SignedReply
		if err := json.Unmarshal(msg.Payload, &reply); err != nil {
			t.Fatalf("failed to decode reply: %v", err)
		}
		msg.Ack()
		return reply, handleErr
	case <-ctx.Done():
		t.Fatal("timed out waiting for a signed reply")
		return certmsg.SignedReply{}, handleErr
	}
}

func TestHandler_ShouldPublishASignedReplyOnSuccess(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	channel := newTestChannel(t)
	h := NewHandler(ks, channel, false, newDefaultTestHandlerLimits())
	job := newTestJob(t)

	reply, err := handleJob(t, h, channel, job)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}

	if reply.Failed() {
		t.Fatalf("expected a success reply, got error %q (%s)", reply.Error, reply.ErrorCode)
	}
	if reply.RequestID != job.RequestID {
		t.Errorf("got RequestID %q, want %q", reply.RequestID, job.RequestID)
	}
	if reply.Certificate == "" {
		t.Error("expected a certificate on a success reply")
	}
}

// TestHandler_ShouldPublishAFailureReplyAndAck is the important one: a
// signing failure must still produce a terminal answer for the waiting
// client, and must ack — nacking would redeliver a job that can never
// succeed, which on gochannel means an immediate infinite loop.
func TestHandler_ShouldPublishAFailureReplyAndAck(t *testing.T) {
	t.Parallel()

	ks := &staticKeySource{err: errors.New("ssh-agent unreachable")}
	channel := newTestChannel(t)
	h := NewHandler(ks, channel, false, newDefaultTestHandlerLimits())
	job := newTestJob(t)

	reply, err := handleJob(t, h, channel, job)
	if err != nil {
		t.Fatalf("expected the message to be acked (nil error), got %v", err)
	}

	if !reply.Failed() {
		t.Fatal("expected a failure reply")
	}
	if reply.ErrorCode != certmsg.ErrCodeCAUnavailable {
		t.Errorf("got error code %q, want %q", reply.ErrorCode, certmsg.ErrCodeCAUnavailable)
	}
	if reply.Certificate != "" {
		t.Errorf("expected no certificate on a failure reply, got %q", reply.Certificate)
	}
	if reply.RequestID != job.RequestID {
		t.Errorf("got RequestID %q, want %q", reply.RequestID, job.RequestID)
	}
}

// failingPublisher is a message.Publisher that always fails, standing in for
// a transport failure — the one case handle must nack rather than ack.
type failingPublisher struct{}

func (failingPublisher) Publish(topic string, messages ...*message.Message) error {
	return errors.New("transport unavailable")
}
func (failingPublisher) Close() error { return nil }

// TestHandler_ShouldNackWhenPublishingTheReplyFails covers the one case
// handle must nack: the job itself was handled (signed or not), but the
// reply never made it out, so the job is genuinely still unhandled.
func TestHandler_ShouldNackWhenPublishingTheReplyFails(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	h := NewHandler(ks, failingPublisher{}, false, newDefaultTestHandlerLimits())
	job := newTestJob(t)

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to encode job: %v", err)
	}

	if err := h.handle(message.NewMessage(watermill.NewUUID(), payload)); err == nil {
		t.Error("handle() error = nil, want a non-nil error to nack the message")
	}
}

// TestHandler_Register wires a Handler into a real Router against an
// in-memory channel end to end: publish a job on SignQueueTopic, and confirm
// the registered consumer signs it and replies on SignedTopic — proof
// Register actually connects the handler to the right topic and subscriber,
// not just that AddConsumerHandler was called with plausible-looking
// arguments.
func TestHandler_Register(t *testing.T) {
	t.Parallel()

	channel := newTestChannel(t)
	ks, _ := newTestKeySource(t)
	h := NewHandler(ks, channel, false, newDefaultTestHandlerLimits())

	router, err := message.NewRouter(message.RouterConfig{}, watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	h.Register(router, channel)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replies, err := channel.Subscribe(ctx, certmsg.SignedTopic)
	if err != nil {
		t.Fatalf("failed to subscribe to replies: %v", err)
	}

	routerDone := make(chan error, 1)
	go func() { routerDone <- router.Run(ctx) }()
	select {
	case <-router.Running():
	case <-ctx.Done():
		t.Fatal("timed out waiting for the router to start")
	}
	t.Cleanup(func() {
		cancel()
		<-routerDone
	})

	job := newTestJob(t)
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to encode job: %v", err)
	}
	if err := channel.Publish(certmsg.SignQueueTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		t.Fatalf("failed to publish job: %v", err)
	}

	select {
	case msg := <-replies:
		var reply certmsg.SignedReply
		if err := json.Unmarshal(msg.Payload, &reply); err != nil {
			t.Fatalf("failed to decode reply: %v", err)
		}
		msg.Ack()
		if reply.RequestID != job.RequestID {
			t.Errorf("got RequestID %q, want %q", reply.RequestID, job.RequestID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for a signed reply via the registered router")
	}
}

func TestHandler_ShouldAckAnUnparseableJobWithoutPublishing(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	channel := newTestChannel(t)
	h := NewHandler(ks, channel, false, newDefaultTestHandlerLimits())

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	replies, err := channel.Subscribe(ctx, certmsg.SignedTopic)
	if err != nil {
		t.Fatalf("failed to subscribe to replies: %v", err)
	}

	// Garbage payload: there's no request ID to reply about, so the only
	// correct move is to ack and log rather than redeliver forever.
	if err := h.handle(message.NewMessage(watermill.NewUUID(), []byte("not json"))); err != nil {
		t.Fatalf("expected the message to be acked (nil error), got %v", err)
	}

	select {
	case msg := <-replies:
		t.Errorf("expected no reply to be published, got %q", msg.Payload)
	case <-ctx.Done():
		// No reply, as expected.
	}
}
