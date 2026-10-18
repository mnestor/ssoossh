//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// nssDirectoryConfig turns the NSS directory on. NSS requires LDAP, but
// nothing here needs a directory to answer: the viewer reads only stored
// rows and never asks LDAP, so the URL names a port nothing listens on
// (discard, refused at once) and every sync is off, which leaves the
// seeded rows exactly as written for the whole test. The login-time LDAP
// lookup fails on the refused port and the login proceeds without it.
const nssDirectoryConfig = `ldap:
  enabled: true
  url: "ldap://127.0.0.1:9"
  base_dn: "dc=example,dc=test"
  user_filter: "(uid={{.Username}})"
  timeout: "1s"
  sync:
    interval: "0s"
nss:
  enabled: true
  sync_interval: "0s"
  full_sync_interval: "0s"
`

// The seeded directory: what the pages are expected to show, named once so
// the seed and the assertions cannot drift apart.
const (
	// nssActiveName is an active account in two groups, devs (its primary
	// gid) and ops.
	nssActiveName = "ada"
	nssActiveUID  = 10001
	// nssServiceUID is a second active account, there so a search has
	// something to leave out.
	nssServiceUID = 10002
	// nssWithheldName is withheld by a uid collision with an entry the sync
	// refused to store.
	nssWithheldName   = "dup.one"
	nssWithheldUID    = 10050
	nssWithheldReason = "uid 10050 is also claimed by uid=dup.two,ou=people,dc=example,dc=test"
	// nssDevsGID's group has a membership row for nssOrphanUID, a uid no
	// stored account holds.
	nssDevsName  = "devs"
	nssDevsGID   = 20001
	nssOpsGID    = 20002
	nssOrphanUID = 10099
	// nssAccountCount is every stored account.
	nssAccountCount = 3
	// nssHistoryAction is the one audit event seeded about ada and devs:
	// ada added to devs after the group was stored.
	nssHistoryAction = "nss.group_members_changed"
)

// Selectors the NSS viewer tests share.
const (
	railNSSLink     = `[data-testid="app-rail"] a[href="/admin/nss"]`
	railAuditLink   = `[data-testid="app-rail"] a[href="/admin/audit"]`
	nssListRow      = `[data-testid="nss-list-row"]`
	nssVerdictWord  = `[data-testid="nss-account-verdict-word"]`
	nssGroupMember  = `[data-testid="nss-group-member"]`
	nssMemberOrphan = `[data-testid="nss-group-member-unstored"]`
	nssHistoryEvent = `[data-testid="audit-event"]`
)

// newNSSDirectoryFixture starts a server with NSS on, the admin and auditor
// groups recognised, and its database in a file the test seeds.
func newNSSDirectoryFixture(t *testing.T) *fixture {
	t.Helper()

	f := newFixture(t, func(o *harness.ServerOptions) {
		o.AdminRequireGroup = e2eAdminGroup
		o.AuditorGroup = e2eAuditorGroup
		o.FileDB = true
		o.ExtraConfigYAML = nssDirectoryConfig
	})
	seedNSSDirectory(t, f.Server.OpenDB(t))
	return f
}

// seedNSSDirectory writes the stored entries a sync of a small directory
// would leave, plus a membership row no sync writes (a uid with no stored
// account), straight into the server's tables. The e2e harness runs no
// LDAP server, and a real sync stores only members it can resolve, so the
// last case could not come from one anyway.
func seedNSSDirectory(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	home := func(name string) *string { h := "/home/" + name; return &h }
	shell := "/bin/bash"
	account := func(name string, uid, gid uint32) model.PosixUser {
		return model.PosixUser{
			ID:           nssSeedID(t),
			UID:          uid,
			Name:         name,
			DN:           "uid=" + name + ",ou=people,dc=example,dc=test",
			GID:          gid,
			Home:         home(name),
			Shell:        &shell,
			Gecos:        name,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastSyncedAt: now,
		}
	}
	group := func(name string, gid uint32) model.PosixGroup {
		return model.PosixGroup{
			ID:           nssSeedID(t),
			GID:          gid,
			Name:         name,
			DN:           "cn=" + name + ",ou=groups,dc=example,dc=test",
			CreatedAt:    now,
			UpdatedAt:    now,
			LastSyncedAt: now,
		}
	}

	withheld := account(nssWithheldName, nssWithheldUID, nssWithheldUID)
	withheld.WithheldAt = &now
	withheld.WithheldReason = nssWithheldReason
	accounts := []model.PosixUser{
		account(nssActiveName, nssActiveUID, nssDevsGID),
		account("svc-backup", nssServiceUID, nssDevsGID),
		withheld,
	}
	groups := []model.PosixGroup{group(nssDevsName, nssDevsGID), group("ops", nssOpsGID)}
	members := []model.PosixGroupMember{
		{GID: nssDevsGID, UID: nssActiveUID},
		{GID: nssDevsGID, UID: nssServiceUID},
		{GID: nssDevsGID, UID: nssOrphanUID},
		{GID: nssOpsGID, UID: nssActiveUID},
	}
	// The collision behind the withheld account, as the sync records one
	// whose other entry it refused to store (no OtherID).
	collision := model.NSSCollision{
		Kind:        model.NSSCollisionKindUID,
		HolderID:    withheld.ID,
		OtherDN:     "uid=dup.two,ou=people,dc=example,dc=test",
		Value:       strconv.Itoa(nssWithheldUID),
		OtherName:   "dup.two",
		OtherNumber: nssWithheldUID,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}

	// One history event, as the sync records adding ada to devs: the event
	// and its refs to both rows, which is what puts it on both pages.
	event := model.AuditEvent{
		ID: nssSeedID(t), CreatedAt: now, Action: nssHistoryAction,
		Payload: `{"action":"` + nssHistoryAction + `","system":true,"detail":{"table":"posix_groups","name":"` + nssDevsName +
			`","gid":` + strconv.Itoa(nssDevsGID) + `,"via":"full_sync","added":[{"uid":` + strconv.Itoa(nssActiveUID) +
			`,"name":"` + nssActiveName + `"}],"removed":[],"added_count":1,"removed_count":0},"occurred_at":"` +
			now.Format(time.RFC3339Nano) + `","v":1}`,
	}
	refs := []model.NSSAuditRef{
		{EventID: event.ID, Kind: model.NSSAuditRefGroup, RowID: groups[0].ID, CreatedAt: now},
		{EventID: event.ID, Kind: model.NSSAuditRefAccount, RowID: accounts[0].ID, CreatedAt: now},
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		for _, rows := range []any{&accounts, &groups, &members, &collision, &event, &refs} {
			if err := tx.Create(rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the NSS directory: %v", err)
	}
}

// nssSeedID is a random row id, the convention PosixUser.ID follows.
func nssSeedID(t *testing.T) string {
	t.Helper()

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generating a row id: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// signInWithGroupsAt opens path in a fresh desktop-sized browser (the rail
// is a column there, not a drawer), signs in as username carrying groups,
// and waits for ready.
func signInWithGroupsAt(t *testing.T, f *fixture, path, ready, username string, groups []string) *harness.Browser {
	t.Helper()

	browser := harness.StartBrowser(t)
	browser.SetViewport(t, desktopWidth, desktopHeight)
	browser.Navigate(t, f.Server.BaseURL+path, `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, username, groups)
	browser.WaitVisible(t, ready)
	return browser
}

// nssAccountLink is the list's link to the account with uid.
func nssAccountLink(uid int) string {
	return `[data-testid="nss-list-link"][href="/admin/nss/accounts/` + strconv.Itoa(uid) + `"]`
}

// TestNSSDirectory_AdminReadsTheStoredDirectory walks an admin through the
// viewer over seeded entries: in from the rail, a search, an active account
// and its group, and a withheld account. One server for every case; a
// browser per case, so a page that hangs fails only its own case.
func TestNSSDirectory_AdminReadsTheStoredDirectory(t *testing.T) {
	f := newNSSDirectoryFixture(t)
	admin := []string{e2eAdminGroup}

	t.Run("should land on the stored accounts when an admin opens the NSS directory from the rail", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/dashboard", railNSSLink, "admin", admin)

		b.Click(t, railNSSLink)
		b.WaitURL(t, "/admin/nss/accounts")
		b.WaitCount(t, nssListRow, nssAccountCount)
	})

	t.Run("should narrow the list and keep the term in the address when a name is searched", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts", nssAccountLink(nssServiceUID), "admin", admin)

		b.Type(t, `[data-testid="nss-search"]`, nssActiveName)
		b.WaitURL(t, "/admin/nss/accounts?q="+nssActiveName)
		b.WaitCount(t, nssListRow, 1)
		// By its text: a filtered row's link carries the filter for its back
		// chip, so its href is no longer the bare account path.
		if got := b.Text(t, `[data-testid="nss-list-link"]`); got != nssActiveName {
			t.Errorf("the one row left is %q, want %q", got, nssActiveName)
		}
	})

	t.Run("should show the served verdict and its groups when an active account is opened", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts/"+strconv.Itoa(nssActiveUID), nssVerdictWord, "admin", admin)

		if got := b.Text(t, nssVerdictWord); got != "Served" {
			t.Errorf("verdict = %q, want Served", got)
		}
		b.WaitCount(t, `[data-testid="nss-account-served-group"]`, 2)
	})

	t.Run("should show the membership change in the history when the account and its group are opened", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts/"+strconv.Itoa(nssActiveUID), nssHistoryEvent, "admin", admin)

		if got := b.Text(t, nssHistoryEvent); !strings.Contains(got, nssHistoryAction) {
			t.Errorf("the account's history row reads %q, want %s", got, nssHistoryAction)
		}
		b.Navigate(t, f.Server.BaseURL+"/admin/nss/groups/"+strconv.Itoa(nssDevsGID), nssHistoryEvent)
		b.WaitCount(t, nssHistoryEvent, 1)
	})

	t.Run("should list the group's members with the unstored one never served when a served group is followed", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts/"+strconv.Itoa(nssActiveUID), `[data-testid="nss-account-served-group"]`, "admin", admin)

		b.ClickTestIDWithText(t, "nss-account-served-group", nssDevsName)
		b.WaitURL(t, "/admin/nss/groups/"+strconv.Itoa(nssDevsGID))
		b.WaitCount(t, nssGroupMember, 3)
		if got := b.Text(t, nssMemberOrphan); !strings.Contains(got, "never served") {
			t.Errorf("the member with no stored account reads %q, want it said to be never served", got)
		}
	})

	t.Run("should show not served with the withheld reason when a withheld account is opened", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts/"+strconv.Itoa(nssWithheldUID), nssVerdictWord, "admin", admin)

		if got := b.Text(t, nssVerdictWord); got != "Not served" {
			t.Errorf("verdict = %q, want Not served", got)
		}
		if got := b.Text(t, `[data-testid="nss-account-verdict-reason"]`); !strings.Contains(got, nssWithheldReason) {
			t.Errorf("the not-served reason reads %q, want it to carry %q", got, nssWithheldReason)
		}
	})
}

// TestNSSDirectory_AuditorReadsTheViewerButNotTheDirectory covers the role
// split: the viewer is the auditor's as much as the admin's, while the
// Directory page beside it stays admin-only.
func TestNSSDirectory_AuditorReadsTheViewerButNotTheDirectory(t *testing.T) {
	f := newNSSDirectoryFixture(t)
	auditor := []string{e2eAuditorGroup}

	t.Run("should offer the rail entry and the stored lists when an auditor signs in", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts", nssAccountLink(nssActiveUID), "auditor", auditor)

		b.WaitVisible(t, railNSSLink)
		b.WaitCount(t, nssListRow, nssAccountCount)
		b.Navigate(t, f.Server.BaseURL+"/admin/nss/groups/"+strconv.Itoa(nssDevsGID), nssMemberOrphan)
	})

	t.Run("should refuse the Directory page when an auditor opens it", func(t *testing.T) {
		signInWithGroupsAt(t, f, "/admin/directory", `[data-testid="directory-access-denied"]`, "auditor", auditor)
	})
}

// TestNSSDirectory_OffHidesTheViewer covers a server with NSS off, the
// product default: no rail entry, and the lists say NSS is off rather than
// showing an empty directory.
func TestNSSDirectory_OffHidesTheViewer(t *testing.T) {
	f := newFixture(t, func(o *harness.ServerOptions) {
		o.AdminRequireGroup = e2eAdminGroup
		o.AuditorGroup = e2eAuditorGroup
	})

	t.Run("should drop the rail entry and show the NSS-off notice when NSS is disabled", func(t *testing.T) {
		b := signInWithGroupsAt(t, f, "/admin/nss/accounts", `[data-testid="nss-list-off"]`, "admin", []string{e2eAdminGroup})

		// The rail's admin group has rendered (its Audit log link is up),
		// so the missing NSS entry is absent rather than not drawn yet.
		b.WaitVisible(t, railAuditLink)
		b.AssertNotPresent(t, railNSSLink)
	})
}
