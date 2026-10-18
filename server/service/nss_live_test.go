//go:build ldaplive

package service

// Test methodology: one run of the NSS directory against a real OpenLDAP,
// behind the ldaplive build tag and skipped unless SSOOSSH_TEST_LDAP_URL
// names a disposable server. The test writes its own entries under a fresh
// OU, drives the sync, an on-demand read and a scheduled run's effects
// through the real dialer, and deletes the OU afterwards. Any OpenLDAP with
// the nis schema (posixAccount, posixGroup) will do, for example:
//
//	docker run --rm -d --name nss-ldap-test -p 127.0.0.1:0:389 \
//	  -e LDAP_ORGANISATION=test -e LDAP_DOMAIN=example.test \
//	  -e LDAP_ADMIN_PASSWORD=test osixia/openldap:1.5.0
//	SSOOSSH_TEST_LDAP_URL=ldap://127.0.0.1:<port> \
//	  go test -tags ldaplive -run NSSLive -v ./server/service/

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/model"
)

// liveEnv reads a setting for the live test, with a default.
func liveEnv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// liveDirectory is an admin connection to the live server, for writing the
// test's entries.
type liveDirectory struct {
	t    *testing.T
	conn *ldap.Conn
	ou   string
}

// add writes one entry.
func (d *liveDirectory) add(dn string, attrs map[string][]string) {
	d.t.Helper()
	req := ldap.NewAddRequest(dn, nil)
	for name, values := range attrs {
		req.Attribute(name, values)
	}
	if err := d.conn.Add(req); err != nil {
		d.t.Fatalf("add %s: %v", dn, err)
	}
}

// account writes one posixAccount under the test OU and returns its DN.
func (d *liveDirectory) account(name string, uid uint32, extra map[string][]string) string {
	dn := "uid=" + name + "," + d.ou
	attrs := map[string][]string{
		"objectClass":   {"inetOrgPerson", "posixAccount"},
		"uid":           {name},
		"cn":            {name},
		"sn":            {name},
		"uidNumber":     {strconv.FormatUint(uint64(uid), 10)},
		"gidNumber":     {strconv.FormatUint(uint64(uid), 10)},
		"homeDirectory": {"/home/" + name},
		"loginShell":    {"/bin/bash"},
	}
	for k, v := range extra {
		attrs[k] = v
	}
	d.add(dn, attrs)
	return dn
}

// group writes one posixGroup under the test OU and returns its DN.
func (d *liveDirectory) group(name string, gid uint32, members ...string) string {
	dn := "cn=" + name + "," + d.ou
	attrs := map[string][]string{
		"objectClass": {"posixGroup"},
		"cn":          {name},
		"gidNumber":   {strconv.FormatUint(uint64(gid), 10)},
	}
	if len(members) > 0 {
		attrs["memberUid"] = members
	}
	d.add(dn, attrs)
	return dn
}

// remove deletes one entry.
func (d *liveDirectory) remove(dn string) {
	d.t.Helper()
	if err := d.conn.Del(ldap.NewDelRequest(dn, nil)); err != nil {
		d.t.Fatalf("delete %s: %v", dn, err)
	}
}

// dropMember removes one memberUid value from a group.
func (d *liveDirectory) dropMember(groupDN, member string) {
	d.t.Helper()
	req := ldap.NewModifyRequest(groupDN, nil)
	req.Delete("memberUid", []string{member})
	if err := d.conn.Modify(req); err != nil {
		d.t.Fatalf("drop %s from %s: %v", member, groupDN, err)
	}
}

// openLiveDirectory binds as the admin and creates a fresh OU, deleted with
// everything under it when the test ends.
func openLiveDirectory(t *testing.T, url, bindDN, password, base string) *liveDirectory {
	t.Helper()
	conn, err := ldap.DialURL(url)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	if err := conn.Bind(bindDN, password); err != nil {
		t.Fatalf("bind as %s: %v", bindDN, err)
	}
	d := &liveDirectory{t: t, conn: conn, ou: fmt.Sprintf("ou=nss-live-%d,%s", time.Now().UnixNano(), base)}
	d.add(d.ou, map[string][]string{"objectClass": {"organizationalUnit"}, "ou": {d.ou[3 : len(d.ou)-len(base)-1]}})
	t.Cleanup(func() {
		result, err := conn.Search(ldap.NewSearchRequest(d.ou, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false, "(objectClass=*)", []string{"1.1"}, nil))
		if err == nil {
			// Children before the OU itself.
			for i := len(result.Entries) - 1; i >= 0; i-- {
				if result.Entries[i].DN != d.ou {
					_ = conn.Del(ldap.NewDelRequest(result.Entries[i].DN, nil))
				}
			}
		}
		_ = conn.Del(ldap.NewDelRequest(d.ou, nil))
		_ = conn.Close()
	})
	return d
}

func TestNSSLive_ShouldSyncARealDirectoryWhenOneIsConfigured(t *testing.T) {
	url := os.Getenv("SSOOSSH_TEST_LDAP_URL")
	if url == "" {
		t.Skip("SSOOSSH_TEST_LDAP_URL is not set; the live LDAP test needs a disposable OpenLDAP")
	}
	bindDN := liveEnv("SSOOSSH_TEST_LDAP_BIND_DN", "cn=admin,dc=example,dc=test")
	password := liveEnv("SSOOSSH_TEST_LDAP_BIND_PASSWORD", "test")
	base := liveEnv("SSOOSSH_TEST_LDAP_BASE_DN", "dc=example,dc=test")
	dir := openLiveDirectory(t, url, bindDN, password, base)

	ada := dir.account("ada.lovelace", 10001, map[string][]string{"displayName": {"Ada Lovelace"}})
	svcBackup := dir.account("svc-backup", 10002, nil)
	eve := dir.account("eve", 10001, nil)
	devs := dir.group("devs", 20001, "ada.lovelace", "svc-backup", "nobody")

	cfg := nssTestConfig()
	cfg.LDAP.URL = url
	cfg.LDAP.BindDN = bindDN
	cfg.LDAP.BindPassword = password
	cfg.LDAP.BaseDN = dir.ou
	cfg.LDAP.IDAttribute = "entryUUID"
	h := newNSSHarness(t, cfg)
	// The real dialer and the real clock: modifyTimestamp is the server's.
	h.svc.dial = dialLDAP
	h.svc.now = time.Now
	ctx := t.Context()

	report := func(step string) {
		var users []model.PosixUser
		h.db.Order("uid, name").Find(&users)
		for _, u := range users {
			t.Logf("%s: account %-14s uid=%d gid=%d withheld=%v missing=%v deleted=%v gecos=%q reason=%q",
				step, u.Name, u.UID, u.GID, u.WithheldAt != nil, u.MissingSince != nil, u.DeletedAt != nil, u.Gecos, u.WithheldReason)
		}
		var groups []model.PosixGroup
		h.db.Order("gid").Find(&groups)
		for _, g := range groups {
			t.Logf("%s: group   %-14s gid=%d members=%v", step, g.Name, g.GID, h.members(t, g.GID))
		}
	}

	// 1. The full sync.
	result, err := h.svc.FullSync(ctx)
	if err != nil {
		t.Fatalf("FullSync() error = %v", err)
	}
	t.Logf("full sync: full=%v accounts=%+v groups=%+v", result.Full, result.Accounts, result.Groups)
	report("after full sync")
	if result.Accounts.Seen != 3 || result.Accounts.Refused != 1 || h.count(t, &model.PosixUser{}, "withheld_at IS NOT NULL") != 1 {
		t.Errorf("accounts = %+v, want three seen and the uid collision refusing one and withholding the other", result.Accounts)
	}
	var holder model.PosixUser
	h.db.Where("withheld_at IS NOT NULL").First(&holder)
	svc := h.accountByName(t, "svc-backup")
	if svc.UserID != nil || !slices.Contains(h.members(t, 20001), svc.UID) {
		t.Errorf("svc-backup = %+v, want a stored service account in devs", svc)
	}

	// 2. An on-demand read of an account created since the sync, with its
	// groups found by the membership search.
	dir.account("late.joiner", 10003, nil)
	dir.group("ops", 20002, "late.joiner")
	found, err := h.svc.RefreshUserByName(ctx, "late.joiner")
	if err != nil || !found {
		t.Fatalf("RefreshUserByName(late.joiner) = %v, %v; want it found", found, err)
	}
	report("after on-demand read of late.joiner")
	if !slices.Contains(h.members(t, 20002), 10003) {
		t.Errorf("ops members = %v, want late.joiner", h.members(t, 20002))
	}

	// 3. A member dropped from a group in the directory loses the row at
	// the next scheduled (incremental) run.
	dir.dropMember(devs, "svc-backup")
	result, err = h.svc.IncrementalSync(ctx)
	if err != nil {
		t.Fatalf("IncrementalSync() error = %v", err)
	}
	t.Logf("incremental sync: full=%v accounts=%+v groups=%+v", result.Full, result.Accounts, result.Groups)
	report("after dropping svc-backup from devs")
	if result.Full || slices.Contains(h.members(t, 20001), 10002) {
		t.Errorf("devs members = %v after an incremental run (full=%v), want svc-backup gone", h.members(t, 20001), result.Full)
	}

	// 4. A deleted entry is missing after the next full run, and the uid
	// collision clears once the entry the sync refused has gone (the
	// holder, even deleted, would keep holding the uid).
	refused := eve
	if holder.Name == "eve" {
		refused = ada
	}
	dir.remove(svcBackup)
	dir.remove(refused)
	result, err = h.svc.FullSync(ctx)
	if err != nil {
		t.Fatalf("FullSync() error = %v", err)
	}
	t.Logf("full sync after deletes: accounts=%+v groups=%+v", result.Accounts, result.Groups)
	report("after deleting svc-backup and the refused entry")
	if h.accountByName(t, "svc-backup").MissingSince == nil {
		t.Error("svc-backup missing_since is unset after its entry was deleted")
	}
	if n := h.count(t, &model.PosixUser{}, "withheld_at IS NOT NULL"); n != 0 {
		t.Errorf("withheld accounts = %d, want the collision cleared", n)
	}
}
