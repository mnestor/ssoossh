package controller

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// TestDisableUserIdempotent verifies disabling an already-disabled user is not an error.
func TestDisableUserIdempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	user := createTestUser(t, db, "testuser")

	// First disable
	db.Model(&user).Update("disabled_at", time.Now())

	// Second disable should not error (idempotent)
	result := db.Model(&model.User{}).
		Where("id = ?", user.ID).
		Update("disabled_at", time.Now())

	if result.Error != nil {
		t.Errorf("idempotent disable failed: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", result.RowsAffected)
	}
}

// TestDisableUserUnknownID verifies disabling a non-existent user gets 0 rows affected.
func TestDisableUserUnknownID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)

	result := db.Model(&model.User{}).
		Where("id = ?", "does-not-exist").
		Update("disabled_at", time.Now())

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if result.RowsAffected != 0 {
		t.Errorf("expected 0 rows affected for non-existent user, got %d", result.RowsAffected)
	}
}

// TestEnableUserIdempotent verifies re-enabling an already-enabled user succeeds.
func TestEnableUserIdempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	user := createTestUser(t, db, "testuser")

	// Enable when already enabled should not error
	result := db.Model(&model.User{}).
		Where("id = ?", user.ID).
		Update("disabled_at", nil)

	if result.Error != nil {
		t.Errorf("idempotent enable failed: %v", result.Error)
	}
}

// TestSweepDisabledUserEnrollmentsDoesNotExpireBeforeGracePeriod verifies
// grace period is respected.
func TestSweepDisabledUserEnrollmentsDoesNotExpireBeforeGracePeriod(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	user := createTestUser(t, db, "testuser")
	gracePeriod := 7 * 24 * time.Hour

	// Disable user just now
	now := time.Now()
	db.Model(&user).Update("disabled_at", now)

	// Create an active enrollment
	enrollment := model.Enrollment{
		ID:        uuid.NewString(),
		Code:      uuid.NewString(),
		PublicKey: "test-key",
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Sweep should NOT expire it yet (disabled_at is recent)
	cutoffTime := now.Add(-gracePeriod)
	result := db.Model(&model.Enrollment{}).
		Where("user_id IN (SELECT id FROM users WHERE disabled_at IS NOT NULL AND disabled_at < ?)", cutoffTime).
		Where("expires_at > ?", time.Now()).
		Update("expires_at", time.Now())

	if result.RowsAffected > 0 {
		t.Error("enrollment should not be expired before grace period elapses")
	}
}

// TestSweepDisabledUserEnrollmentsExpiresAfterGracePeriod verifies
// grace period works correctly.
func TestSweepDisabledUserEnrollmentsExpiresAfterGracePeriod(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	user := createTestUser(t, db, "testuser")
	gracePeriod := 7 * 24 * time.Hour

	// Disable user well in the past
	now := time.Now()
	disabledAt := now.Add(-gracePeriod).Add(-1 * time.Hour)
	db.Model(&user).Update("disabled_at", disabledAt)

	// Create an active enrollment
	enrollment := model.Enrollment{
		ID:        uuid.NewString(),
		Code:      uuid.NewString(),
		PublicKey: "test-key",
		UserID:    user.ID,
		CreatedAt: disabledAt,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Sweep should expire it
	cutoffTime := now.Add(-gracePeriod)
	result := db.Model(&model.Enrollment{}).
		Where("user_id IN (SELECT id FROM users WHERE disabled_at IS NOT NULL AND disabled_at < ?)", cutoffTime).
		Where("expires_at > ?", time.Now()).
		Update("expires_at", now)

	if result.RowsAffected != 1 {
		t.Errorf("expected 1 enrollment to expire, got %d", result.RowsAffected)
	}
}

// Helper functions.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.AutoMigrate(&model.User{}, &model.Enrollment{})
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, username string) model.User {
	t.Helper()
	user := model.User{
		ID:        uuid.NewString(),
		Subject:   uuid.NewString(),
		Username:  username,
		Email:     username + "@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	db.Create(&user)
	return user
}
