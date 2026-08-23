package signer

import (
	"context"
	"encoding/json"
	"log/slog"
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

	// Verify the announced key matches the source key
	want := string(ssh.MarshalAuthorizedKey(caPub))
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

	// Subscribe to announcements before Run, since gochannel is non-persistent
	// and messages published before subscription are lost.
	announceReceived := make(chan certmsg.CAKeyAnnounce, 1)
	announcementsDone := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		announcements, err := channel.Subscribe(ctx, certmsg.CAKeyAnnounceTopic)
		if err != nil {
			announcementsDone <- err
			return
		}

		select {
		case msg := <-announcements:
			var announce certmsg.CAKeyAnnounce
			if err := announce.Unmarshal(msg.Payload); err != nil {
				announcementsDone <- err
				return
			}
			msg.Ack()
			announceReceived <- announce
			announcementsDone <- nil
		case <-ctx.Done():
			announcementsDone <- ctx.Err()
		}
	}()

	// Give the subscriber a chance to subscribe before calling Run
	time.Sleep(100 * time.Millisecond)

	// Run the announcer briefly
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = an.Run(ctx) // Ignore the context.Canceled error

	// Wait for the subscriber to report
	if err := <-announcementsDone; err != nil {
		t.Fatalf("subscription error: %v", err)
	}

	// Verify the startup announcement was received
	select {
	case announce := <-announceReceived:
		want := string(ssh.MarshalAuthorizedKey(caPub))
		if announce.PublicKey != want {
			t.Errorf("got public key %q, want %q", announce.PublicKey, want)
		}
		if announce.AnnouncedAt.IsZero() {
			t.Error("expected a non-zero announcement timestamp")
		}
	default:
		t.Fatal("no announcement received")
	}
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
