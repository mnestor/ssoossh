package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestUserDB creates an in-memory test database.
func newTestUserDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	return db
}

// TestCheckUserDisabled_FirstTimeLoginNoRow verifies that a first-time login
// (no users row for this subject) succeeds even if the database lookup occurs.
// This is NOT an error condition — the user legitimately has no row yet.
func TestCheckUserDisabled_FirstTimeLoginNoRow(t *testing.T) {
	t.Parallel()
	db := newTestUserDB(t)
	db.AutoMigrate(&model.User{})

	service := &AuthService{db: db}
	ctx := context.Background()

	// First-time login: subject does not exist in users table
	err := service.checkUserDisabled(ctx, "subject-never-seen-before")

	// Must succeed: no row does not mean disabled
	if err != nil {
		t.Errorf("checkUserDisabled for non-existent subject failed: %v", err)
	}
}

// TestCheckUserDisabled_DatabaseErrorFailsClosed verifies that a genuine
// database error during the disabled lookup fails closed — the session is NOT
// established. This is an authorization decision: a database blip must not
// admit a user an admin has explicitly disabled.
func TestCheckUserDisabled_DatabaseErrorFailsClosed(t *testing.T) {
	t.Parallel()
	db := newTestUserDB(t)
	db.AutoMigrate(&model.User{})

	service := &AuthService{db: db}
	ctx := context.Background()

	// Create a user with a subject
	subject := uuid.NewString()
	user := model.User{
		ID:       uuid.NewString(),
		Subject:  subject,
		Username: "testuser",
		Email:    "test@example.com",
	}
	db.Create(&user)

	// Simulate a database error by closing the connection
	// This will make any query fail
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	sqlDB.Close()

	// Query should fail with database error
	err = service.checkUserDisabled(ctx, subject)

	// Must NOT be nil — database errors must fail closed
	if err == nil {
		t.Error("checkUserDisabled must fail closed on database error")
	}
}

// TestCheckUserDisabled_DisabledUserBlocked verifies that a disabled user
// cannot establish a session.
func TestCheckUserDisabled_DisabledUserBlocked(t *testing.T) {
	t.Parallel()
	db := newTestUserDB(t)
	db.AutoMigrate(&model.User{})

	service := &AuthService{db: db}
	ctx := context.Background()

	// Create a disabled user
	subject := uuid.NewString()
	now := time.Now()
	user := model.User{
		ID:         uuid.NewString(),
		Subject:    subject,
		Username:   "testuser",
		Email:      "test@example.com",
		DisabledAt: &now,
	}
	db.Create(&user)

	// Check should reject the disabled user
	err := service.checkUserDisabled(ctx, subject)

	// Must return UserDisabledError
	if err == nil {
		t.Error("checkUserDisabled must reject disabled user")
	}
	if _, ok := err.(*errorresponses.UserDisabledError); !ok {
		t.Errorf("checkUserDisabled returned %T, want UserDisabledError", err)
	}
}

// TestCheckUserDisabled_EnabledUserAllowed verifies that an enabled (not disabled)
// user can establish a session.
func TestCheckUserDisabled_EnabledUserAllowed(t *testing.T) {
	t.Parallel()
	db := newTestUserDB(t)
	db.AutoMigrate(&model.User{})

	service := &AuthService{db: db}
	ctx := context.Background()

	// Create an enabled user (DisabledAt is nil)
	subject := uuid.NewString()
	user := model.User{
		ID:       uuid.NewString(),
		Subject:  subject,
		Username: "testuser",
		Email:    "test@example.com",
	}
	db.Create(&user)

	// Check should allow the enabled user
	err := service.checkUserDisabled(ctx, subject)

	if err != nil {
		t.Errorf("checkUserDisabled must allow enabled user, got: %v", err)
	}
}
