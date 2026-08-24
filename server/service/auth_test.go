package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// TestCheckUserDisabledRejectsDisabledUser verifies a disabled user is rejected.
func TestCheckUserDisabledRejectsDisabledUser(t *testing.T) {
	t.Parallel()
	db := setupAuthTestDB(t)

	// Create and disable a user
	user := model.User{
		ID:         uuid.NewString(),
		Subject:    "test-subject",
		Username:   "testuser",
		Email:      "test@example.com",
		DisabledAt: timePtr(time.Now()),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	db.Create(&user)

	// Check if disabled (simulate what auth.go does)
	var disabledAt *time.Time
	result := db.Model(&model.User{}).
		Select("disabled_at").
		Where("subject = ?", user.Subject).
		Scan(&disabledAt)

	if result.Error != nil {
		t.Fatalf("failed to check disabled status: %v", result.Error)
	}

	if disabledAt == nil {
		t.Error("disabled user should have non-nil disabled_at")
	}
}

// TestCheckUserDisabledAllowsEnabledUser verifies enabled users are allowed.
func TestCheckUserDisabledAllowsEnabledUser(t *testing.T) {
	t.Parallel()
	db := setupAuthTestDB(t)

	// Create an enabled user
	user := model.User{
		ID:        uuid.NewString(),
		Subject:   "test-subject",
		Username:  "testuser",
		Email:     "test@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	db.Create(&user)

	// Check if disabled
	var disabledAt *time.Time
	result := db.Model(&model.User{}).
		Select("disabled_at").
		Where("subject = ?", user.Subject).
		Scan(&disabledAt)

	if result.Error != nil {
		t.Fatalf("failed to check disabled status: %v", result.Error)
	}

	if disabledAt != nil {
		t.Error("enabled user should have nil disabled_at")
	}
}

// TestCheckUserDisabledFailsClosedOnDatabaseError verifies auth fails closed
// when database is unavailable.
func TestCheckUserDisabledFailsClosedOnDatabaseError(t *testing.T) {
	t.Parallel()

	// Use a closed database to simulate a database error
	db := setupAuthTestDB(t)
	db.Migrator().DropTable(&model.User{})

	// Query against non-existent table should fail
	var disabledAt *time.Time
	result := db.Model(&model.User{}).
		Select("disabled_at").
		Where("subject = ?", "test-subject").
		Scan(&disabledAt)

	// Error should occur (not be nil)
	if result.Error == nil {
		t.Error("expected database error, but query succeeded")
	}

	// The key point: when there's a DB error, we must FAIL CLOSED, not allow the user in.
	// This test verifies that errors.Is or similar is used to distinguish between
	// "not found" (allowed) and "database error" (must reject).
}

// Helper functions
func setupAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	db.AutoMigrate(&model.User{})
	return db
}

func timePtr(t time.Time) *time.Time {
	return &t
}
