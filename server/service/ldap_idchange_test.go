package service

// Test methodology: the warning a sync logs when the entry at a stored DN
// carries another unique id, on the fakeDirectory seam ldap_test.go uses.
// It is the only signal that ldap.id_attribute changed under a database
// holding ids from another attribute, since there is no migration tool.

import (
	"context"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/model"
)

// idEntry builds alice's entry carrying one id attribute, with the raw
// bytes set the way the directory library hands them back.
func idEntry(attr, id string) *ldap.Entry {
	return &ldap.Entry{
		DN: "uid=alice,ou=people,dc=test",
		Attributes: []*ldap.EntryAttribute{
			{Name: "uid", Values: []string{"alice"}, ByteValues: [][]byte{[]byte("alice")}},
			{Name: attr, Values: []string{id}, ByteValues: [][]byte{[]byte(id)}},
		},
	}
}

func TestSync_ShouldWarnWhenTheEntryAtTheStoredDNHasAnotherID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		storedID string
		entryID  string
		wantWarn bool
	}{
		{"should warn when the id at the stored DN changed", "old-uuid", "new-uuid", true},
		{"should stay quiet when the id is unchanged", "same-uuid", "same-uuid", false},
		{"should stay quiet when no id was stored yet", "", "new-uuid", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := ldapTestConfig(nil)
			cfg.LDAP.IDAttribute = "entryUUID"
			dir := &fakeDirectory{}
			svc, db := newLDAPTestService(t, cfg, dir)
			userID := seedLDAPUser(t, db, "sub-alice", "alice")
			row := model.UserLDAP{UserID: userID, DN: "uid=alice,ou=people,dc=test", DirectoryID: tt.storedID}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("seed bookkeeping: %v", err)
			}

			// The id search finds nothing (the stored id belongs to an
			// attribute or entry that is gone); the DN read finds alice.
			current := idEntry("entryUUID", tt.entryID)
			dir.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
				if req.Scope == ldap.ScopeBaseObject || !strings.Contains(req.Filter, "entryUUID=") || (tt.storedID == tt.entryID) {
					return &ldap.SearchResult{Entries: []*ldap.Entry{current}}, nil
				}
				return &ldap.SearchResult{}, nil
			}
			out := captureSyncLog(svc)

			if err := svc.Sync(context.Background()); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			warned := strings.Contains(out.String(), "has a different unique id")
			if warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v; log:\n%s", warned, tt.wantWarn, out)
			}
		})
	}
}
