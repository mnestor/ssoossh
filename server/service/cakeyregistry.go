package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

// CAKeyRegistry persists and manages the CA public key registry. Multiple
// signers may announce the same CA key (HA setup); the registry deduplicates
// by computing the fingerprint server-side and normalizing the key to
// canonical form.
type CAKeyRegistry struct {
	db  *gorm.DB
	ttl time.Duration
}

// NewCAKeyRegistry constructs a CAKeyRegistry backed by db, with keys expiring
// after ttl of inactivity.
func NewCAKeyRegistry(db *gorm.DB, ttl time.Duration) *CAKeyRegistry {
	return &CAKeyRegistry{
		db:  db,
		ttl: ttl,
	}
}

// Upsert parses ann.PublicKey (ssh.ParseAuthorizedKey), re-marshals it to
// canonical form, computes its SHA256 fingerprint, and upserts the row
// keyed on that fingerprint with ExpiresAt = now + ttl. This guarantees
// deduplication: N signers sharing one CA key, or the same key with
// whitespace/comment differences, all land on the same single row.
//
// Returns an error if the public key cannot be parsed or the database
// operation fails.
func (r *CAKeyRegistry) Upsert(ctx context.Context, ann certmsg.CAKeyAnnounce) error {
	// Parse the announced public key to validate it. The comment, options,
	// and trailing bytes ParseAuthorizedKey also returns carry nothing this
	// registry stores; the canonical re-marshal below drops them anyway.
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ann.PublicKey)) //nolint:dogsled // see comment above
	if err != nil {
		return fmt.Errorf("parse announced public key: %w", err)
	}

	// Re-marshal to canonical form (normalized, no comments)
	canonical := strings.TrimRight(string(ssh.MarshalAuthorizedKey(pubKey)), "\n")

	// Compute the fingerprint server-side (the dedup key)
	fingerprint := ssh.FingerprintSHA256(pubKey)

	// Upsert keyed on fingerprint
	expiresAt := time.Now().Add(r.ttl)
	key := model.CASignerKey{
		Fingerprint: fingerprint,
		PublicKey:   canonical,
		ExpiresAt:   expiresAt,
	}

	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			UpdateAll: true,
		}).
		Create(&key).Error; err != nil {
		return fmt.Errorf("upsert ca signer key: %w", err)
	}

	return nil
}

// ActiveKeys returns the canonical public keys of all non-expired CA keys in
// the registry, ordered stably by fingerprint.
func (r *CAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error) {
	var keys []model.CASignerKey

	if err := r.db.WithContext(ctx).
		Where("expires_at > ?", time.Now()).
		Order("fingerprint ASC").
		Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list active ca signer keys: %w", err)
	}

	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = key.PublicKey
	}

	return result, nil
}

// DeleteExpired removes all expired CA keys from the registry, returning the
// count of rows deleted. This is hygiene, not correctness — ActiveKeys never
// returns expired rows, so the sweep is safe at any cadence.
func (r *CAKeyRegistry) DeleteExpired(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("expires_at <= ?", time.Now()).
		Delete(&model.CASignerKey{})

	if result.Error != nil {
		return 0, fmt.Errorf("delete expired ca signer keys: %w", result.Error)
	}

	return result.RowsAffected, nil
}

// SweepExpired is a wrapper around DeleteExpired for the job scheduler,
// which expects a function that takes context and returns error. It discards
// the row count.
func (r *CAKeyRegistry) SweepExpired(ctx context.Context) error {
	_, err := r.DeleteExpired(ctx)
	return err
}

// CAKeyListener consumes CAKeyAnnounceTopic messages and upserts them into
// the registry.
type CAKeyListener struct {
	reg *CAKeyRegistry
}

// NewCAKeyListener constructs a CAKeyListener backed by reg.
func NewCAKeyListener(reg *CAKeyRegistry) *CAKeyListener {
	return &CAKeyListener{reg: reg}
}

// Register adds the CA key listener to router, consuming messages from
// subscriber on the CAKeyAnnounceTopic.
func (l *CAKeyListener) Register(router *message.Router, subscriber message.Subscriber) {
	router.AddConsumerHandler("ca-key-listener", certmsg.CAKeyAnnounceTopic, subscriber, l.handle)
}

// handle processes one CA key announcement.
func (l *CAKeyListener) handle(msg *message.Message) error {
	var ann certmsg.CAKeyAnnounce
	if err := ann.Unmarshal(msg.Payload); err != nil {
		// Unparseable message; ack it and move on (re-delivery won't help)
		return nil
	}

	if err := l.reg.Upsert(msg.Context(), ann); err != nil {
		// Database or parse error; nack to retry
		return err
	}

	return nil
}
