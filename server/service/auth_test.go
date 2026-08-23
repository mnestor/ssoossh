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

func TestExtraClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claims  map[string]any
		mapping map[string]string
		want    map[string]extraValue
	}{
		{
			name:    "should return nil when no extras are configured",
			claims:  map[string]any{"dept": "eng"},
			mapping: nil,
			want:    nil,
		},
		{
			name:    "should extract a string claim as a scalar",
			claims:  map[string]any{"department": "eng"},
			mapping: map[string]string{"dept": "department"},
			want:    map[string]extraValue{"dept": scalarExtra("eng")},
		},
		{
			name:    "should coerce a bool claim to a string",
			claims:  map[string]any{"isAdmin": true},
			mapping: map[string]string{"admin": "isAdmin"},
			want:    map[string]extraValue{"admin": scalarExtra("true")},
		},
		{
			name:    "should coerce an integral number claim without a decimal point",
			claims:  map[string]any{"level": float64(42)},
			mapping: map[string]string{"level": "level"},
			want:    map[string]extraValue{"level": scalarExtra("42")},
		},
		{
			name:    "should coerce a fractional number claim",
			claims:  map[string]any{"score": 1.5},
			mapping: map[string]string{"score": "score"},
			want:    map[string]extraValue{"score": scalarExtra("1.5")},
		},
		{
			name:    "should extract a string array claim as a list",
			claims:  map[string]any{"alts": []any{"a", "b"}},
			mapping: map[string]string{"accounts": "alts"},
			want:    map[string]extraValue{"accounts": listExtra([]string{"a", "b"})},
		},
		{
			name:    "should skip non-string elements in an array claim",
			claims:  map[string]any{"alts": []any{"a", 7, "b"}},
			mapping: map[string]string{"accounts": "alts"},
			want:    map[string]extraValue{"accounts": listExtra([]string{"a", "b"})},
		},
		{
			name:    "should store empty when the claim is absent",
			claims:  map[string]any{},
			mapping: map[string]string{"dept": "department"},
			want:    map[string]extraValue{"dept": scalarExtra("")},
		},
		{
			name:    "should store empty when the claim is an unsupported shape",
			claims:  map[string]any{"department": map[string]any{"nested": true}},
			mapping: map[string]string{"dept": "department"},
			want:    map[string]extraValue{"dept": scalarExtra("")},
		},
		{
			name:    "should store empty when the claim is JSON null",
			claims:  map[string]any{"department": nil},
			mapping: map[string]string{"dept": "department"},
			want:    map[string]extraValue{"dept": scalarExtra("")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extraClaims(tt.claims, tt.mapping)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d extras, want %d (%+v)", len(got), len(tt.want), got)
			}
			for name, want := range tt.want {
				if got[name].String() != want.String() {
					t.Errorf("extra %q = %q, want %q", name, got[name].String(), want.String())
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

	// AutoMigrate rather than raw DDL: newTestDB can hand back either
	// sqlite or a live Postgres (SSOOSSH_TEST_POSTGRES_DSN), and hand-written
	// column types like DATETIME only exist on one of them.
	if err := db.AutoMigrate(&model.User{}); err != nil {
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
