package service

// Test methodology: Unit tests for OIDC claim parsing and user upsert
// logic. Tests run in parallel (t.Parallel()). Table-driven for
// stringSliceClaim's cases. upsertUser is exercised against a real
// in-memory sqlite *gorm.DB (a minimal ad-hoc users table, not the full
// embedded migration set - this tests upsertUser's query logic, not
// migration correctness, which server/bootstrap/db_test.go already covers).

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

func TestStringSliceClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		claims   map[string]any
		key      string
		required bool
		want     []string
		wantErr  bool
	}{
		{
			name:   "should return nil when key is unconfigured",
			claims: map[string]any{"groups": []any{"a"}},
			key:    "",
			want:   nil,
		},
		{
			name:   "should return string values when the claim is a string array",
			claims: map[string]any{"groups": []any{"admins", "devs"}},
			key:    "groups",
			want:   []string{"admins", "devs"},
		},
		{
			name:   "should skip non-string entries in the array",
			claims: map[string]any{"groups": []any{"admins", 5, "devs"}},
			key:    "groups",
			want:   []string{"admins", "devs"},
		},
		{
			name:     "should error when a required claim is absent",
			claims:   map[string]any{},
			key:      "groups",
			required: true,
			wantErr:  true,
		},
		{
			name:     "should error when a required claim isn't an array",
			claims:   map[string]any{"groups": "not-an-array"},
			key:      "groups",
			required: true,
			wantErr:  true,
		},
		{
			// An optional claim the provider does not send is the common
			// case, not a misconfiguration: it warns and yields nothing.
			name:   "should return nil when an optional claim is absent",
			claims: map[string]any{},
			key:    "groups",
			want:   nil,
		},
		{
			name:   "should return nil when an optional claim isn't an array",
			claims: map[string]any{"groups": "not-an-array"},
			key:    "groups",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := stringSliceClaim(tt.claims, tt.key, tt.required)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// newTestUserDB opens an in-memory sqlite *gorm.DB with a minimal users
// table matching model.User's columns.
func newTestUserDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := newTestDB(t)

	err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		subject TEXT NOT NULL UNIQUE,
		username TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '',
		other_accounts TEXT NOT NULL DEFAULT '',
		service_accounts TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error
	if err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}

	return db
}

func TestAuthService_upsertUser_ShouldInsertThenUpdateOnRepeatLogin(t *testing.T) {
	t.Parallel()

	db := newTestUserDB(t)
	s := &AuthService{db: db}

	first := &Identity{Subject: "sub-1", Username: "alice", Email: "alice@example.com"}
	if err := s.upsertUser(context.Background(), first); err != nil {
		t.Fatalf("unexpected error on first login: %v", err)
	}

	var row model.User
	if err := db.First(&row, "subject = ?", "sub-1").Error; err != nil {
		t.Fatalf("failed to load inserted user: %v", err)
	}
	if row.Username != "alice" {
		t.Errorf("got username %q, want %q", row.Username, "alice")
	}
	firstID := row.ID

	second := &Identity{Subject: "sub-1", Username: "alice2", Email: "alice2@example.com", Groups: []string{"ignored"}}
	if err := s.upsertUser(context.Background(), second); err != nil {
		t.Fatalf("unexpected error on second login: %v", err)
	}

	var count int64
	if err := db.Model(&model.User{}).Where("subject = ?", "sub-1").Count(&count).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one row for subject sub-1 after a repeat login, got %d", count)
	}

	if err := db.First(&row, "subject = ?", "sub-1").Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if row.Username != "alice2" {
		t.Errorf("got username %q after update, want %q", row.Username, "alice2")
	}
	if row.ID != firstID {
		t.Errorf("expected the user's ID to stay stable across logins, got %q then %q", firstID, row.ID)
	}
}
