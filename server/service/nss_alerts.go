package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/model"
)

// claimNSSAlert reports whether the caller may raise the NSS alert kind for
// subject now, at most once per window across every instance. Every
// instance runs every job and serves every lookup, so the dedupe cannot
// live in memory: the claim is one upsert on nss_alerts that inserts the
// row, or moves last_raised_at forward only when the previous raise is at
// least window old. Exactly one caller sees a row affected, and only that
// caller writes the audit event. now is normalised to UTC because SQLite
// compares the stored timestamps as text (see server/dbtime).
func claimNSSAlert(ctx context.Context, db *gorm.DB, kind, subject string, now time.Time, window time.Duration) (bool, error) {
	now = now.UTC()
	row := model.NSSAlert{Kind: kind, Subject: subject, LastRaisedAt: now}
	result := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}, {Name: "subject"}},
		DoUpdates: clause.Assignments(map[string]any{"last_raised_at": now}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "nss_alerts.last_raised_at <= ?", Vars: []any{now.Add(-window)}},
		}},
	}).Create(&row)
	if result.Error != nil {
		return false, fmt.Errorf("claim nss alert %s for %s: %w", kind, subject, result.Error)
	}
	return result.RowsAffected == 1, nil
}
