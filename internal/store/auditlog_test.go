// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestAuditLogStore(t *testing.T) *GormAuditLogStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory DB: %v", err)
	}
	s, err := NewGormAuditLogStore(db)
	if err != nil {
		t.Fatalf("failed to create audit log store: %v", err)
	}
	return s
}

func TestGormAuditLogStore_CreateAndList(t *testing.T) {
	s := newTestAuditLogStore(t)

	entry := &AuditLogEntry{
		RequestID: "req-1",
		UserName:  "alice",
		Decision:  "approved",
		PublicKey: "ssh-ed25519 AAAA...",
		Account:   "alice",
		CertType:  "user",
	}

	if err := s.Create(entry); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if entry.ID == "" {
		t.Error("expected ID to be set after Create")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set after Create")
	}

	entries, err := s.ListByUser("alice")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Decision != "approved" {
		t.Errorf("expected decision 'approved', got %q", entries[0].Decision)
	}
}

func TestGormAuditLogStore_EmptyList(t *testing.T) {
	s := newTestAuditLogStore(t)

	entries, err := s.ListByUser("nobody")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for unknown user, got %d", len(entries))
	}
}

func TestGormAuditLogStore_MultiUserIsolation(t *testing.T) {
	s := newTestAuditLogStore(t)

	_ = s.Create(&AuditLogEntry{RequestID: "r1", UserName: "alice", Decision: "approved", CertType: "user"})
	_ = s.Create(&AuditLogEntry{RequestID: "r2", UserName: "bob", Decision: "rejected", CertType: "user"})
	_ = s.Create(&AuditLogEntry{RequestID: "r3", UserName: "alice", Decision: "rejected", CertType: "user"})

	aliceEntries, err := s.ListByUser("alice")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(aliceEntries) != 2 {
		t.Errorf("expected 2 entries for alice, got %d", len(aliceEntries))
	}

	bobEntries, err := s.ListByUser("bob")
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(bobEntries) != 1 {
		t.Errorf("expected 1 entry for bob, got %d", len(bobEntries))
	}
}
