package service

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
