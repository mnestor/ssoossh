package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// Why a presented key id names no usable key, when it names one at all.
const (
	// NSSDeadKeyRevoked is a revoked key.
	NSSDeadKeyRevoked = "revoked"
	// NSSDeadKeyExpired is a key past its expires_at.
	NSSDeadKeyExpired = "expired"
)

// NSSKeyVerifier is what the fleet key middleware needs: one check of a
// presented key. NSSKeyService implements it; an interface so the
// middleware's ordering and limits can be tested without a database.
type NSSKeyVerifier interface {
	VerifyFleetKey(ctx context.Context, keyID, secret string) (NSSKeyVerdict, error)
}

// NSSKeyVerdict is the outcome of checking one presented fleet key.
type NSSKeyVerdict struct {
	// KeyID is the id presented, verified or not. It may be logged.
	KeyID string
	// OK is true when the secret matched a live key's current secret, or
	// its previous one inside the rotation grace.
	OK bool
	// Generation is the generation the matching secret belongs to: the
	// key's current one, or one less for the previous secret. Zero unless
	// OK.
	Generation int
	// Dead is NSSDeadKeyRevoked or NSSDeadKeyExpired when the id names a
	// key that can no longer be used, else empty. Set whatever the secret,
	// since a dead key's secret is never compared.
	Dead string
	// Key is the row the id names, nil when it names none. For a dead key
	// it is what the attempt is reported against.
	Key *model.NSSKey
}

// VerifyFleetKey checks a presented key id and secret, the steps in
// docs/proposals/nss-directory/1-server-api.md, "Fleet key
// authentication": look the id up; for a missing, revoked or expired key
// compare against a dummy hash anyway; otherwise compare the secret's
// SHA-256 with the current hash and the previous one while its grace
// lasts, both in constant time. Exactly two comparisons happen on every
// path, so an unknown id, a dead key and a wrong secret cost the same and
// the caller answers all of them with the same 401.
//
// An error is the database failing, never a refused key.
func (s *NSSKeyService) VerifyFleetKey(ctx context.Context, keyID, secret string) (NSSKeyVerdict, error) {
	presented := []byte(hashNSSSecret(secret))
	verdict := NSSKeyVerdict{KeyID: keyID}

	var key model.NSSKey
	err := s.db.WithContext(ctx).Where("id = ?", keyID).Take(&key).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return NSSKeyVerdict{}, fmt.Errorf("read fleet key: %w", err)
	}
	found := err == nil
	now := s.now()

	current, previous := s.dummyHash, s.dummyHash
	if found {
		verdict.Key = &key
		switch nssKeyStatus(&key, now) {
		case model.NSSKeyStatusRevoked:
			verdict.Dead = NSSDeadKeyRevoked
		case model.NSSKeyStatusExpired:
			verdict.Dead = NSSDeadKeyExpired
		default:
			current = key.SecretHash
			if key.PreviousSecretHash != nil && key.PreviousExpiresAt != nil && key.PreviousExpiresAt.After(now) {
				previous = *key.PreviousSecretHash
			}
		}
	}

	matchesCurrent := subtle.ConstantTimeCompare(presented, []byte(current)) == 1
	matchesPrevious := subtle.ConstantTimeCompare(presented, []byte(previous)) == 1
	switch {
	case !found || verdict.Dead != "":
		// The comparisons above were against the dummy hash, which nothing
		// matches; they ran only so this path costs what the others do.
	case matchesCurrent:
		verdict.OK = true
		verdict.Generation = key.CurrentGeneration
	case matchesPrevious && previous != s.dummyHash:
		verdict.OK = true
		verdict.Generation = key.CurrentGeneration - 1
	}
	return verdict, nil
}
