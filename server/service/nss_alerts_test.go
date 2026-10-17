package service

import (
	"context"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/testdb"
)

// TestClaimNSSAlert walks one sequence of claims against a single database,
// so each step sees the rows the earlier ones left.
func TestClaimNSSAlert(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	steps := []struct {
		name    string
		kind    string
		subject string
		at      time.Time
		window  time.Duration
		want    bool
	}{
		{"should claim when the alert was never raised", "uid_collision", "1234", start, day, true},
		{"should refuse when the same alert was raised inside the window", "uid_collision", "1234", start.Add(time.Hour), day, false},
		{"should claim when another subject has its own alert", "uid_collision", "5678", start.Add(time.Hour), day, true},
		{"should claim when another kind shares the subject", "name_collision", "1234", start.Add(time.Hour), day, true},
		{"should refuse when the window has not quite passed", "uid_collision", "1234", start.Add(day - time.Second), day, false},
		{"should claim when exactly one window has passed", "uid_collision", "1234", start.Add(day), day, true},
		{"should refuse again when the new raise starts a new window", "uid_collision", "1234", start.Add(day + time.Minute), day, false},
		{"should claim every time when the window is zero", "uid_collision", "1234", start.Add(day + time.Minute), 0, true},
		{"should claim when the caller passes a non-UTC time past the window", "uid_collision", "1234", start.Add(3 * day).In(time.FixedZone("EST", -5*3600)), day, true},
	}
	for _, step := range steps {
		got, err := claimNSSAlert(ctx, db, step.kind, step.subject, step.at, step.window)
		if err != nil {
			t.Fatalf("%s: claimNSSAlert() error = %v", step.name, err)
		}
		if got != step.want {
			t.Fatalf("%s: claimNSSAlert() = %v, want %v", step.name, got, step.want)
		}
	}
}

// TestClaimNSSAlert_ShouldReturnAnErrorWhenTheTableIsMissing covers the
// failure path with a database that has no schema.
func TestClaimNSSAlert_ShouldReturnAnErrorWhenTheTableIsMissing(t *testing.T) {
	db := testdb.Empty(t)
	if _, err := claimNSSAlert(context.Background(), db, "uid_collision", "1", time.Now(), time.Hour); err == nil {
		t.Fatal("claimNSSAlert() error = nil, want an error for the missing table")
	}
}
