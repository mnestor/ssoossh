package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

// generateTestPublicKey returns an authorized_keys-formatted public key for
// testing.
func generateTestPublicKey(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	// Parse back as a public key
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	return strings.TrimRight(string(ssh.MarshalAuthorizedKey(signer.PublicKey())), "\n")
}

// newTestCAKeyRegistry opens an in-memory sqlite DB migrated for
// model.CASignerKey and returns a CAKeyRegistry backed by it with the given
// TTL.
func newTestCAKeyRegistry(t *testing.T, ttl time.Duration) *CAKeyRegistry {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CASignerKey{}); err != nil {
		t.Fatalf("failed to migrate ca_signer_keys table: %v", err)
	}

	reg := NewCAKeyRegistry(db, ttl)
	if reg == nil {
		t.Fatal("NewCAKeyRegistry returned nil")
	}
	return reg
}

func TestCAKeyRegistry_Upsert_ShouldInsertNewKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newTestCAKeyRegistry(t, time.Hour)

	pubKey := generateTestPublicKey(t)
	ann := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey,
		AnnouncedAt: time.Now(),
	}

	err := reg.Upsert(ctx, ann)
	if err != nil {
		t.Fatalf("unexpected error from Upsert: %v", err)
	}

	// Verify the key was inserted
	keys, err := reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error from ActiveKeys: %v", err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
}

func TestCAKeyRegistry_Upsert_ShouldRefreshExpiryOnReannounce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ttl := time.Hour
	reg := newTestCAKeyRegistry(t, ttl)

	pubKey := generateTestPublicKey(t)
	now := time.Now()

	// First announce
	ann1 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey,
		AnnouncedAt: now,
	}

	err := reg.Upsert(ctx, ann1)
	if err != nil {
		t.Fatalf("unexpected error from first Upsert: %v", err)
	}

	var key1 model.CASignerKey
	if err := reg.db.First(&key1).Error; err != nil {
		t.Fatalf("failed to fetch key after first upsert: %v", err)
	}

	// Move time forward and re-announce
	time.Sleep(100 * time.Millisecond)
	ann2 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey,
		AnnouncedAt: now.Add(100 * time.Millisecond),
	}

	err = reg.Upsert(ctx, ann2)
	if err != nil {
		t.Fatalf("unexpected error from second Upsert: %v", err)
	}

	var key2 model.CASignerKey
	if err := reg.db.First(&key2).Error; err != nil {
		t.Fatalf("failed to fetch key after second upsert: %v", err)
	}

	// Expiry should have advanced
	if key2.ExpiresAt.Before(key1.ExpiresAt) {
		t.Errorf("expiry should have advanced, got before: %v < %v", key2.ExpiresAt, key1.ExpiresAt)
	}
}

func TestCAKeyRegistry_Upsert_ShouldRejectUnparseablePublicKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newTestCAKeyRegistry(t, time.Hour)

	ann := certmsg.CAKeyAnnounce{
		PublicKey:   "not a valid public key",
		AnnouncedAt: time.Now(),
	}

	err := reg.Upsert(ctx, ann)
	if err == nil {
		t.Fatal("expected error for unparseable public key, got nil")
	}
}

func TestCAKeyRegistry_Upsert_ShouldDedupBySameKeyWithDifferentComment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newTestCAKeyRegistry(t, time.Hour)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	// Generate two announced keys with the same public key but different
	// comments (whitespace variations)
	pubKeyBytes := ssh.MarshalAuthorizedKey(signer.PublicKey())
	pubKey1 := strings.TrimRight(string(pubKeyBytes), "\n")
	pubKey2 := strings.TrimRight(string(pubKeyBytes), "\n") + " comment1"

	ann1 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey1,
		AnnouncedAt: time.Now(),
	}

	err = reg.Upsert(ctx, ann1)
	if err != nil {
		t.Fatalf("unexpected error from first Upsert: %v", err)
	}

	// Second announce with different comment
	ann2 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey2,
		AnnouncedAt: time.Now().Add(time.Second),
	}

	err = reg.Upsert(ctx, ann2)
	if err != nil {
		t.Fatalf("unexpected error from second Upsert: %v", err)
	}

	// Should have exactly one row (deduplicated by fingerprint)
	var count int64
	if err := reg.db.Model(&model.CASignerKey{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count keys: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 key after dedup, got %d", count)
	}
}

func TestCAKeyRegistry_ActiveKeys_ShouldExcludeExpiredKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ttl := 100 * time.Millisecond
	reg := newTestCAKeyRegistry(t, ttl)

	pubKey := generateTestPublicKey(t)
	ann := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey,
		AnnouncedAt: time.Now(),
	}

	err := reg.Upsert(ctx, ann)
	if err != nil {
		t.Fatalf("unexpected error from Upsert: %v", err)
	}

	// Should be active now
	keys, err := reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error from ActiveKeys: %v", err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 active key, got %d", len(keys))
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Should be excluded now
	keys, err = reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error from ActiveKeys after expiry: %v", err)
	}

	if len(keys) != 0 {
		t.Errorf("expected 0 active keys after expiry, got %d", len(keys))
	}
}

func TestCAKeyRegistry_DeleteExpired_ShouldRemoveOnlyExpiredKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ttl := 100 * time.Millisecond
	reg := newTestCAKeyRegistry(t, ttl)

	pubKey1 := generateTestPublicKey(t)
	ann1 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey1,
		AnnouncedAt: time.Now(),
	}

	err := reg.Upsert(ctx, ann1)
	if err != nil {
		t.Fatalf("unexpected error from first Upsert: %v", err)
	}

	// Wait for the first key to expire
	time.Sleep(150 * time.Millisecond)

	// Add a new key that won't expire
	pubKey2 := generateTestPublicKey(t)
	ann2 := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey2,
		AnnouncedAt: time.Now(),
	}

	err = reg.Upsert(ctx, ann2)
	if err != nil {
		t.Fatalf("unexpected error from second Upsert: %v", err)
	}

	// Delete expired
	deleted, err := reg.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("unexpected error from DeleteExpired: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted key, got %d", deleted)
	}

	// Verify only 1 key remains
	keys, err := reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error from ActiveKeys: %v", err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 key remaining, got %d", len(keys))
	}
}

func TestCAKeyListener_ShouldHandleAnnounceMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newTestCAKeyRegistry(t, time.Hour)
	listener := NewCAKeyListener(reg)

	pubKey := generateTestPublicKey(t)
	ann := certmsg.CAKeyAnnounce{
		PublicKey:   pubKey,
		AnnouncedAt: time.Now(),
	}

	data, err := ann.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal announce: %v", err)
	}

	// Create a message and call the handler directly
	msg := message.NewMessage("test-uuid", data)
	msg.SetContext(ctx)

	err = listener.handle(msg)
	if err != nil {
		t.Fatalf("unexpected error from listener handle: %v", err)
	}

	// Verify the key was inserted
	keys, err := reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("unexpected error from ActiveKeys: %v", err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 key after listener processing, got %d", len(keys))
	}
}

// SweepExpired is the shape the job scheduler calls: context in, error out,
// row count discarded. It is a thin wrapper, but it is the only entry point
// the scheduler uses -- if it stopped deleting, nothing else would notice,
// because DeleteExpired's own tests call DeleteExpired.
func TestCAKeyRegistry_SweepExpired_ShouldDeleteExpiredKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := newTestCAKeyRegistry(t, 50*time.Millisecond)

	if err := reg.Upsert(ctx, certmsg.CAKeyAnnounce{
		PublicKey:   generateTestPublicKey(t),
		AnnouncedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := reg.SweepExpired(ctx); err != nil {
		t.Fatalf("SweepExpired() error = %v", err)
	}

	keys, err := reg.ActiveKeys(ctx)
	if err != nil {
		t.Fatalf("ActiveKeys() error = %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected the expired key to be swept, got %d remaining", len(keys))
	}
}

// Register is the wiring between the announce topic and handle. The
// handler-level test calls handle directly, so nothing else proves the
// listener is actually subscribed to CAKeyAnnounceTopic -- a wrong topic
// name here would leave every instance's registry silently empty.
func TestCAKeyListener_Register_ShouldConsumeFromTheAnnounceTopic(t *testing.T) {
	t.Parallel()

	reg := newTestCAKeyRegistry(t, time.Hour)

	logger := watermill.NewSlogLogger(slog.Default())
	channel := gochannel.NewGoChannel(gochannel.Config{}, logger)
	t.Cleanup(func() { _ = channel.Close() })

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: time.Second}, logger)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	NewCAKeyListener(reg).Register(router, channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := router.Run(ctx); err != nil {
			t.Errorf("router stopped with an error: %v", err)
		}
	}()
	<-router.Running()

	ann := certmsg.CAKeyAnnounce{
		PublicKey:   generateTestPublicKey(t),
		AnnouncedAt: time.Now(),
	}
	payload, err := ann.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := channel.Publish(certmsg.CAKeyAnnounceTopic, message.NewMessage("1", payload)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		keys, err := reg.ActiveKeys(context.Background())
		if err != nil {
			t.Fatalf("ActiveKeys() error = %v", err)
		}
		if len(keys) == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("announced key never reached the registry via %s", certmsg.CAKeyAnnounceTopic)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
