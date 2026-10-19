package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// The fleet key mail. Every message goes to the key's notification address
// (see NotificationService.NotifyNSSKey) and is always sent. The ones sent
// by a sweep or on a repeated event are claimed on a column of nss_keys with
// a guarded UPDATE first, so every instance can run every sweep and see
// every attempt without any of them sending twice. All of it is off when
// mail.enabled is false: the sweeps are not registered, the attempt claims
// are not taken, and the notifier discards the rest.

// nssRotationCleanupInterval is how often the rotation cleanup digest
// repeats once the quiet period is over.
const nssRotationCleanupInterval = 24 * time.Hour

// nssCleanupListLimit caps the addresses the cleanup digest lists; the
// rotation report has the rest.
const nssCleanupListLimit = 50

// nssKeyReminderBatch bounds the keys one sweep pass claims. Keys are
// fleet-sized, tens of rows, so this is a backstop, not a throughput knob.
const nssKeyReminderBatch = 500

// notifyRotated mails the key's address that it was rotated, by whom, and
// how long the previous secret keeps working.
func (s *NSSKeyService) notifyRotated(ctx context.Context, key *model.NSSKey, actor *nssActor) {
	s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyRotated, key.ID, &notify.NSSKeyRotated{
		KeyID:             key.ID,
		KeyName:           key.Name,
		Generation:        key.CurrentGeneration,
		RotatedAt:         derefTime(key.RotatedAt),
		RotatedByUsername: actor.user.Username,
		RotatedByName:     actor.displayName(),
		PreviousExpiresAt: derefTime(key.PreviousExpiresAt),
		ExpiresAt:         derefTime(key.ExpiresAt),
		ServerURL:         s.config.HTTP.PublicOrigin(),
	})
}

// notifyRevoked mails the key's address that it was revoked, and by whom.
func (s *NSSKeyService) notifyRevoked(ctx context.Context, key *model.NSSKey, actor *nssActor) {
	s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyRevoked, key.ID, &notify.NSSKeyRevoked{
		KeyID:             key.ID,
		KeyName:           key.Name,
		RevokedAt:         derefTime(key.RevokedAt),
		RevokedByUsername: actor.user.Username,
		RevokedByName:     actor.displayName(),
		ServerURL:         s.config.HTTP.PublicOrigin(),
	})
}

// derefTime is t, or the zero time for nil.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// notifyDeadKeyAttempt mails the key's address that a revoked or expired
// key was presented, at most once per mail.expired_attempt_window across
// every instance. One claim column serves both kinds: a key is revoked or
// expired, and a host presenting it is the same news either way.
//
// Claim-then-publish, as the service code's expired-attempt mail does, so
// a crash between the two loses a message rather than doubling one; the
// next attempt in the host's retry loop reports it again.
func (s *NSSKeyService) notifyDeadKeyAttempt(ctx context.Context, attempt *nssDeadKeyAttempt) {
	window := s.config.Mail.ExpiredAttemptWindow
	if !s.config.Mail.Enabled || window <= 0 {
		return
	}
	at := attempt.at.UTC()
	result := s.db.WithContext(ctx).Model(&model.NSSKey{}).
		Where("id = ?", attempt.key.ID).
		Where("last_expired_attempt_notified_at IS NULL OR last_expired_attempt_notified_at < ?", at.Add(-window)).
		Update("last_expired_attempt_notified_at", at)
	if result.Error != nil {
		slog.ErrorContext(ctx, "failed to claim the fleet key attempt notification",
			"key_id", attempt.key.ID, "error", result.Error)
		return
	}
	if result.RowsAffected == 0 {
		return
	}

	if attempt.dead == NSSDeadKeyRevoked {
		s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyRevokedAttempt, attempt.key.ID, &notify.NSSKeyRevokedAttempt{
			KeyID:       attempt.key.ID,
			KeyName:     attempt.key.Name,
			SourceIP:    attempt.sourceIP,
			AttemptedAt: attempt.at,
			RevokedAt:   derefTime(attempt.key.RevokedAt),
			ServerURL:   s.config.HTTP.PublicOrigin(),
		})
		return
	}
	s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyExpiredAttempt, attempt.key.ID, &notify.NSSKeyExpiredAttempt{
		KeyID:       attempt.key.ID,
		KeyName:     attempt.key.Name,
		SourceIP:    attempt.sourceIP,
		AttemptedAt: attempt.at,
		ExpiredAt:   derefTime(attempt.key.ExpiresAt),
		ServerURL:   s.config.HTTP.PublicOrigin(),
	})
}

// SweepExpiryReminders sends nss_key_expiring for live keys within
// mail.expiry_reminder_lead of expiring, on the service code reminder's
// cadence: weekly, then daily over the final ExpiryReminderDailyWindow.
// Each send is claimed on expiry_reminder_sent_at. A key that never
// expires is never due. A scheduled job; a zero lead sends nothing.
func (s *NSSKeyService) SweepExpiryReminders(ctx context.Context) error {
	lead := s.config.Mail.ExpiryReminderLead
	if !s.config.Mail.Enabled || lead <= 0 {
		return nil
	}

	now := s.now().UTC()
	dailyFrom := now.Add(ExpiryReminderDailyWindow)
	var due []model.NSSKey
	err := s.db.WithContext(ctx).
		Where("revoked_at IS NULL").
		Where("expires_at > ? AND expires_at <= ?", now, now.Add(lead)).
		Where(s.db.
			Where("expires_at <= ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)",
				dailyFrom, now.Add(-ExpiryReminderDailyInterval)).
			Or("expires_at > ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)",
				dailyFrom, now.Add(-ExpiryReminderWeeklyInterval))).
		Order("expires_at").
		Limit(nssKeyReminderBatch).
		Find(&due).Error
	if err != nil {
		return fmt.Errorf("find fleet keys due an expiry reminder: %w", err)
	}

	sent := 0
	for _, key := range due {
		daily := !key.ExpiresAt.After(dailyFrom)
		interval := ExpiryReminderWeeklyInterval
		if daily {
			interval = ExpiryReminderDailyInterval
		}
		result := s.db.WithContext(ctx).Model(&model.NSSKey{}).
			Where("id = ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)", key.ID, now.Add(-interval)).
			Update("expiry_reminder_sent_at", now)
		if result.Error != nil {
			return fmt.Errorf("claim the expiry reminder for fleet key %q: %w", key.ID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}
		sent++
		s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyExpiring, key.ID, &notify.NSSKeyExpiring{
			KeyID:       key.ID,
			KeyName:     key.Name,
			Description: key.Description,
			GroupName:   key.GroupName,
			ExpiresAt:   derefTime(key.ExpiresAt),
			Daily:       daily,
			ServerURL:   s.config.HTTP.PublicOrigin(),
		})
	}
	if sent > 0 {
		slog.InfoContext(ctx, "sent fleet key expiry reminders", "count", sent, "lead", lead)
	}
	return nil
}

// SweepRotationCleanup sends nss_key_rotation_cleanup for rotated keys
// whose hosts have not all switched: silent for mail.rotation_quiet_period
// after the rotation, then daily while any address still presents the
// previous secret and the grace has not ended. Each send is claimed on
// rotation_cleanup_sent_at, which a rotation clears; a key whose hosts have
// all switched is not claimed, so it is looked at again next pass. A
// scheduled job; a zero quiet period sends nothing.
func (s *NSSKeyService) SweepRotationCleanup(ctx context.Context) error {
	quiet := s.config.Mail.RotationQuietPeriod
	if !s.config.Mail.Enabled || quiet <= 0 {
		return nil
	}

	now := s.now().UTC()
	var due []model.NSSKey
	err := s.db.WithContext(ctx).
		Where("revoked_at IS NULL AND rotated_at IS NOT NULL AND rotated_at <= ?", now.Add(-quiet)).
		Where("previous_expires_at IS NOT NULL AND previous_expires_at > ?", now).
		Where("rotation_cleanup_sent_at IS NULL OR rotation_cleanup_sent_at <= ?", now.Add(-nssRotationCleanupInterval)).
		Order("rotated_at").
		Limit(nssKeyReminderBatch).
		Find(&due).Error
	if err != nil {
		return fmt.Errorf("find rotated fleet keys due a cleanup digest: %w", err)
	}

	for i := range due {
		key := &due[i]
		report, err := s.rotationReport(ctx, key)
		if err != nil {
			return err
		}
		if report.NotSwitched == 0 {
			continue
		}
		result := s.db.WithContext(ctx).Model(&model.NSSKey{}).
			Where("id = ? AND current_generation = ?", key.ID, key.CurrentGeneration).
			Where("rotation_cleanup_sent_at IS NULL OR rotation_cleanup_sent_at <= ?", now.Add(-nssRotationCleanupInterval)).
			Update("rotation_cleanup_sent_at", now)
		if result.Error != nil {
			return fmt.Errorf("claim the rotation cleanup digest for fleet key %q: %w", key.ID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}
		s.notifier.NotifyNSSKey(ctx, notify.KindNSSKeyRotationCleanup, key.ID, cleanupPayload(key, report, s.config.HTTP.PublicOrigin()))
	}
	return nil
}

// cleanupPayload builds the digest from the rotation report: the counts,
// and the not_switched addresses most recently seen first, capped at
// nssCleanupListLimit. The report already orders not_switched rows first
// and by last seen.
func cleanupPayload(key *model.NSSKey, report *NSSRotationReport, serverURL string) *notify.NSSKeyRotationCleanup {
	payload := &notify.NSSKeyRotationCleanup{
		KeyID:                key.ID,
		KeyName:              key.Name,
		Generation:           key.CurrentGeneration,
		RotatedAt:            derefTime(key.RotatedAt),
		PreviousExpiresAt:    derefTime(key.PreviousExpiresAt),
		Switched:             report.Switched,
		NotSwitched:          report.NotSwitched,
		Gone:                 report.Gone,
		NotSwitchedAddresses: []string{},
		ServerURL:            serverURL,
	}
	for _, row := range report.Rows {
		if row.Bucket != model.NSSRotationBucketNotSwitched {
			continue
		}
		if len(payload.NotSwitchedAddresses) == nssCleanupListLimit {
			payload.MoreNotSwitched++
			continue
		}
		payload.NotSwitchedAddresses = append(payload.NotSwitchedAddresses, row.SourceIP)
	}
	return payload
}
