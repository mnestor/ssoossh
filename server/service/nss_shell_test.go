package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

func TestNSSShell_ShouldReturnTheLinkedAccountWhenThePersonHasOne(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	aliceID := seedNSSPerson(t, db, "sub-alice", "alice", nil)
	seedNSSPerson(t, db, "sub-bob", "bob", nil)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: new(uint32(10001)), UserID: &aliceID, ShellPref: ptr("zsh")})
	lookups := newTestNSSLookup(nssLookupTestConfig(), db, nil)

	tests := []struct {
		name    string
		subject string
		want    *PosixAccount
	}{
		{name: "should return the account when a posix row carries the person's id", subject: "sub-alice",
			want: &PosixAccount{UID: 10001, Name: "alice", ShellPref: ptr("zsh")}},
		{name: "should return none when no posix row is linked to the person", subject: "sub-bob"},
		{name: "should return none when the session has no users row", subject: "sub-nobody"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lookups.AccountForIdentity(t.Context(), &Identity{Subject: tt.subject})
			if err != nil {
				t.Fatalf("AccountForIdentity: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("account = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNSSShell_ShouldValidateAndStoreTheChoiceWhenAPersonSetsTheirShell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subject    string
		stored     *string
		pref       *string
		wantPref   *string
		wantErr    func(error) bool
		wantAudits int
	}{
		{name: "should store the shell when it is allowed", subject: "sub-alice", pref: ptr("fish"), wantPref: ptr("fish"), wantAudits: 1},
		{name: "should clear the shell when the choice is null", subject: "sub-alice", stored: ptr("zsh"), pref: nil, wantPref: nil, wantAudits: 1},
		{name: "should write nothing when the choice is what is stored", subject: "sub-alice", stored: ptr("zsh"), pref: ptr("zsh"), wantPref: ptr("zsh")},
		{
			name: "should refuse the shell with shell_not_allowed when it is not in the list", subject: "sub-alice",
			stored: ptr("zsh"), pref: ptr("tcsh"), wantPref: ptr("zsh"),
			wantErr: func(err error) bool {
				var invalid *errorresponses.InvalidRequestError
				return errors.As(err, &invalid) && strings.HasPrefix(invalid.Reason, "shell_not_allowed")
			},
		},
		{
			name: "should refuse a path when only bare names are allowed", subject: "sub-alice",
			pref: ptr("/bin/bash"),
			wantErr: func(err error) bool {
				var invalid *errorresponses.InvalidRequestError
				return errors.As(err, &invalid) && strings.HasPrefix(invalid.Reason, "shell_not_allowed")
			},
		},
		{
			name: "should refuse an empty name when it is sent instead of null", subject: "sub-alice",
			pref: ptr(""),
			wantErr: func(err error) bool {
				var invalid *errorresponses.InvalidRequestError
				return errors.As(err, &invalid)
			},
		},
		{
			name: "should answer not found when no account is linked to the person", subject: "sub-bob", pref: ptr("zsh"),
			wantErr: func(err error) bool {
				var notFound *errorresponses.NotFoundError
				return errors.As(err, &notFound)
			},
		},
		{
			name: "should refuse the session when it has no users row", subject: "sub-nobody", pref: ptr("zsh"),
			wantErr: func(err error) bool {
				var forbidden *errorresponses.ForbiddenError
				return errors.As(err, &forbidden)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			aliceID := seedNSSPerson(t, db, "sub-alice", "alice", nil)
			seedNSSPerson(t, db, "sub-bob", "bob", nil)
			seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: new(uint32(10001)), UserID: &aliceID, ShellPref: tt.stored})
			c := nssLookupTestConfig()
			lookups := newTestNSSLookup(c, db, nil)
			lookups.SetAuditor(NewAuditService(c, db))

			got, err := lookups.SetShellPref(t.Context(), &Identity{Subject: tt.subject, Username: "alice"}, tt.pref, "192.0.2.7")
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Errorf("err = %v, not the expected kind", err)
				}
			} else if err != nil {
				t.Fatalf("SetShellPref: %v", err)
			} else if !reflect.DeepEqual(got.ShellPref, tt.wantPref) {
				t.Errorf("returned shell_pref = %s, want %s", deref(got.ShellPref), deref(tt.wantPref))
			}

			var row model.PosixUser
			if err := db.Where("name = ?", "alice").Take(&row).Error; err != nil {
				t.Fatalf("read back: %v", err)
			}
			want := tt.wantPref
			if tt.wantErr != nil {
				want = tt.stored
			}
			if !reflect.DeepEqual(row.ShellPref, want) {
				t.Errorf("stored shell_pref = %s, want %s", deref(row.ShellPref), deref(want))
			}

			var events []model.AuditEvent
			if err := db.Where("action = ?", string(AuditNSSShellChanged)).Find(&events).Error; err != nil {
				t.Fatalf("read audit: %v", err)
			}
			if len(events) != tt.wantAudits {
				t.Fatalf("audit rows = %d, want %d", len(events), tt.wantAudits)
			}
			if tt.wantAudits == 1 {
				var payload struct {
					Detail map[string]any `json:"detail"`
				}
				if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
					t.Fatalf("decode audit: %v", err)
				}
				if payload.Detail["before"] != optionalValue(tt.stored) || payload.Detail["after"] != optionalValue(tt.pref) {
					t.Errorf("audit detail = %v, want before %v and after %v", payload.Detail, optionalValue(tt.stored), optionalValue(tt.pref))
				}
				if events[0].SourceIP != "192.0.2.7" || events[0].ActorUserID == nil || *events[0].ActorUserID != aliceID {
					t.Errorf("audit row = %+v, want source 192.0.2.7 and actor %s", events[0], aliceID)
				}
			}
		})
	}
}

func TestNSSShell_ShouldReturnACopyWhenAskedForTheAllowedShells(t *testing.T) {
	t.Parallel()

	lookups := newTestNSSLookup(nssLookupTestConfig(), testdb.New(t), nil)
	shells := lookups.AllowedShells()
	shells[0] = "changed"
	if got := lookups.AllowedShells(); !reflect.DeepEqual(got, []string{"sh", "bash", "zsh", "fish"}) {
		t.Errorf("AllowedShells() = %v after editing a returned copy, want the configured list", got)
	}
}
