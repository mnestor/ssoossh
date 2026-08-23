package signer

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/certmsg"
)

// Test methodology: mirror handler_test.go's approach — exercising the
// announcer's core logic directly (Register and Run) against a real Router
// with in-memory gochannel, without lifecycle scaffolding that would obscure
// what's being tested. The three test cases correspond to the brief's three
// behaviors: request-driven re-announce, startup announce, and periodic
// announce.

// receiveAnnounce subscribes to CAKeyAnnounceTopic and waits for the next
// message, timing out after 2 seconds. Returns the parsed announce or fails
// the test.
func receiveAnnounce(t *testing.T, channel *gochannel.GoChannel) certmsg.CAKeyAnnounce {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	announcements, err := channel.Subscribe(ctx, certmsg.CAKeyAnnounceTopic)
	if err != nil {
		t.Fatalf("failed to subscribe to announcements: %v", err)
	}

	select {
	case msg := <-announcements:
		var announce certmsg.CAKeyAnnounce
		if err := announce.Unmarshal(msg.Payload); err != nil {
			t.Fatalf("failed to decode announcement: %v", err)
		}
		msg.Ack()
		return announce
	case <-ctx.Done():
		t.Fatal("timed out waiting for an announcement")
		return certmsg.CAKeyAnnounce{}
	}
}

// newRequestChannel publishes a request to CAKeyRequestTopic on the given
// channel, triggering a re-announce from the announcer.
func newRequestChannel(t *testing.T, channel *gochannel.GoChannel) {
	t.Helper()

	payload, err := json.Marshal(map[string]interface{}{})
	if err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}

	if err := channel.Publish(certmsg.CAKeyRequestTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		t.Fatalf("failed to publish request: %v", err)
	}
}

func TestAnnouncer_ShouldAnnounceOnRequestMessage(t *testing.T) {
	t.Parallel()

	ks, caPub := newTestKeySource(t)
	channel := newTestChannel(t)
	an := NewAnnouncer(ks, channel, 1*time.Hour)

	router, err := message.NewRouter(message.RouterConfig{}, watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	an.Register(router, channel)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

	// Publish a request to the CAKeyRequestTopic
	newRequestChannel(t, channel)

	// Receive the announcement triggered by the request
	announce := receiveAnnounce(t, channel)

	// Verify the announced key matches the source key (in trimmed authorized_keys form)
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(caPub)))
	if announce.PublicKey != want {
		t.Errorf("got public key %q, want %q", announce.PublicKey, want)
	}
	if announce.AnnouncedAt.IsZero() {
		t.Error("expected a non-zero announcement timestamp")
	}
}

func TestAnnouncer_ShouldAnnounceAtStartup(t *testing.T) {
	t.Parallel()

	ks, caPub := newTestKeySource(t)
	channel := newTestChannel(t)
	an := NewAnnouncer(ks, channel, 1*time.Hour)

	// Start the announcer directly (not via Router) to test Run's startup announce.
	// Use a longer timeout than we need to avoid context timeout being the limiting
	// factor in the test.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start Run in a goroutine and collect its result
	runDone := make(chan error, 1)
	go func() {
		runDone <- an.Run(ctx)
	}()

	// Wait for an announcement to be published
	announce := receiveAnnounce(t, channel)

	// Verify the startup announcement was received with the correct key
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(caPub)))
	if announce.PublicKey != want {
		t.Errorf("got public key %q, want %q", announce.PublicKey, want)
	}
	if announce.AnnouncedAt.IsZero() {
		t.Error("expected a non-zero announcement timestamp")
	}

	// Clean up: wait for Run to exit when context is canceled
	cancel()
	<-runDone
}

func TestAnnouncer_ShouldAnnounceMarshaledAuthorizedKeysForm(t *testing.T) {
	t.Parallel()

	ks, caPub := newTestKeySource(t)
	channel := newTestChannel(t)
	an := NewAnnouncer(ks, channel, 1*time.Hour)

	router, err := message.NewRouter(message.RouterConfig{}, watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	an.Register(router, channel)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

	// Publish a request to trigger an announcement
	newRequestChannel(t, channel)

	// Receive the announcement
	announce := receiveAnnounce(t, channel)

	// Parse the announced key and verify it's a valid public key in authorized_keys format
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(announce.PublicKey))
	if err != nil {
		t.Fatalf("failed to parse announced public key: %v", err)
	}

	// Verify it matches the source key
	if string(pubKey.Marshal()) != string(caPub.Marshal()) {
		t.Error("announced key does not match the source key")
	}
}
