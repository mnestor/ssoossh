package service

// Test methodology: the NSS directory is exercised against a real database
// (testdb, so make test-postgres runs the same tests on Postgres) and a
// fake directory at the ldapConn seam. Unlike fakeDirectory in
// ldap_test.go, nssFakeDirectory evaluates the filters it is sent, because
// the code under test builds them (an equality ANDed onto nss.user_filter,
// a modifyTimestamp bound, a membership search) and the results only mean
// something if the directory answers them the way a real one would.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/testdb"
)

// nssFakeDirectory is a directory that answers searches by evaluating
// their filters over a set of entries.
type nssFakeDirectory struct {
	mu      sync.Mutex
	entries []*ldap.Entry
	// dialErr, when set, makes the dialer fail: a directory outage.
	dialErr error
	// fail, when set, may refuse a search by returning an error.
	fail func(req *ldap.SearchRequest) error
	// filters records every filter searched, in order, and paged how many
	// of the searches were paged.
	filters []string
	paged   int
}

// dialer returns a dialer serving this fake.
func (f *nssFakeDirectory) dialer() ldapDialer {
	return func(*config.LDAPConfig) (ldapConn, error) {
		if f.dialErr != nil {
			return nil, f.dialErr
		}
		return f, nil
	}
}

// Search answers one search: a base-scope read by DN, or a subtree search
// under the base, each filtered, and cut at the size limit with the error
// a directory returns.
func (f *nssFakeDirectory) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searchLocked(req, req.SizeLimit)
}

// SearchWithPaging answers a paged search all at once, with no size limit.
func (f *nssFakeDirectory) SearchWithPaging(req *ldap.SearchRequest, _ uint32) (*ldap.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paged++
	return f.searchLocked(req, 0)
}

// Close does nothing.
func (f *nssFakeDirectory) Close() error { return nil }

// searchLocked is Search with the lock held.
func (f *nssFakeDirectory) searchLocked(req *ldap.SearchRequest, sizeLimit int) (*ldap.SearchResult, error) {
	f.filters = append(f.filters, req.Filter)
	if f.fail != nil {
		if err := f.fail(req); err != nil {
			return nil, err
		}
	}
	match, err := parseTestFilter(req.Filter)
	if err != nil {
		return nil, ldap.NewError(ldap.LDAPResultFilterError, err)
	}

	var out []*ldap.Entry
	if req.Scope == ldap.ScopeBaseObject {
		found := false
		for _, e := range f.entries {
			if strings.EqualFold(e.DN, req.BaseDN) {
				found = true
				if match(e) {
					out = append(out, e)
				}
			}
		}
		if !found {
			return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("no such object"))
		}
		return &ldap.SearchResult{Entries: out}, nil
	}
	for _, e := range f.entries {
		if strings.HasSuffix(strings.ToLower(e.DN), strings.ToLower(req.BaseDN)) && match(e) {
			out = append(out, e)
		}
	}
	if sizeLimit > 0 && len(out) > sizeLimit {
		return &ldap.SearchResult{Entries: out[:sizeLimit]},
			ldap.NewError(ldap.LDAPResultSizeLimitExceeded, errors.New("size limit exceeded"))
	}
	return &ldap.SearchResult{Entries: out}, nil
}

// put adds e, replacing any entry with its DN.
func (f *nssFakeDirectory) put(e *ldap.Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, old := range f.entries {
		if strings.EqualFold(old.DN, e.DN) {
			f.entries[i] = e
			return
		}
	}
	f.entries = append(f.entries, e)
}

// remove deletes the entry with dn.
func (f *nssFakeDirectory) remove(dn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, e := range f.entries {
		if strings.EqualFold(e.DN, dn) {
			f.entries = append(f.entries[:i], f.entries[i+1:]...)
			return
		}
	}
}

// searched returns the filters recorded so far.
func (f *nssFakeDirectory) searched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.filters...)
}

// parseTestFilter compiles an RFC 4515 filter into a matcher, for the
// subset the NSS code sends: &, |, !, equality, >=, <= and presence.
// Attribute names compare without regard to case and values with
// caseIgnoreMatch, as the common directory schemas do.
func parseTestFilter(filter string) (func(*ldap.Entry) bool, error) {
	if _, err := ldap.CompileFilter(filter); err != nil {
		return nil, err
	}
	m, rest, err := parseTestFilterAt(filter)
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return nil, fmt.Errorf("trailing filter text %q", rest)
	}
	return m, nil
}

// parseTestFilterAt parses one parenthesised filter at the start of s.
func parseTestFilterAt(s string) (func(*ldap.Entry) bool, string, error) {
	if !strings.HasPrefix(s, "(") {
		return nil, "", fmt.Errorf("expected ( at %q", s)
	}
	s = s[1:]
	switch {
	case strings.HasPrefix(s, "&"), strings.HasPrefix(s, "|"):
		and := s[0] == '&'
		s = s[1:]
		var parts []func(*ldap.Entry) bool
		for strings.HasPrefix(s, "(") {
			m, rest, err := parseTestFilterAt(s)
			if err != nil {
				return nil, "", err
			}
			parts, s = append(parts, m), rest
		}
		if !strings.HasPrefix(s, ")") {
			return nil, "", fmt.Errorf("expected ) at %q", s)
		}
		return func(e *ldap.Entry) bool {
			for _, p := range parts {
				if p(e) != and {
					return !and
				}
			}
			return and
		}, s[1:], nil
	case strings.HasPrefix(s, "!"):
		m, rest, err := parseTestFilterAt(s[1:])
		if err != nil || !strings.HasPrefix(rest, ")") {
			return nil, "", fmt.Errorf("bad not filter: %w", err)
		}
		return func(e *ldap.Entry) bool { return !m(e) }, rest[1:], nil
	}

	end := strings.Index(s, ")")
	if end < 0 {
		return nil, "", fmt.Errorf("unterminated item %q", s)
	}
	item, rest := s[:end], s[end+1:]
	op := "="
	i := strings.Index(item, "=")
	attr, raw := item[:i], item[i+1:]
	if strings.HasSuffix(attr, ">") || strings.HasSuffix(attr, "<") {
		op = attr[len(attr)-1:] + "="
		attr = attr[:len(attr)-1]
	}
	if op == "=" && raw == "*" {
		return func(e *ldap.Entry) bool { return len(e.GetEqualFoldAttributeValues(attr)) > 0 }, rest, nil
	}
	value, err := unescapeTestFilterValue(raw)
	if err != nil {
		return nil, "", err
	}
	return func(e *ldap.Entry) bool {
		for _, v := range e.GetEqualFoldAttributeValues(attr) {
			lv, lw := strings.ToLower(v), strings.ToLower(value)
			switch {
			case op == "=" && lv == lw, op == ">=" && lv >= lw, op == "<=" && lv <= lw:
				return true
			}
		}
		return false
	}, rest, nil
}

// unescapeTestFilterValue reverses RFC 4515 value escaping.
func unescapeTestFilterValue(raw string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			b.WriteByte(raw[i])
			continue
		}
		if i+3 > len(raw) {
			return "", fmt.Errorf("bad escape in %q", raw)
		}
		decoded, err := hex.DecodeString(raw[i+1 : i+3])
		if err != nil {
			return "", fmt.Errorf("bad escape in %q: %w", raw, err)
		}
		b.Write(decoded)
		i += 2
	}
	return b.String(), nil
}

// nssClock is a settable clock for the service's now.
type nssClock struct {
	mu sync.Mutex
	t  time.Time
}

// now returns the clock's time.
func (c *nssClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance moves the clock forward by d.
func (c *nssClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// nssTestStart is the fixed instant every NSS test clock starts at.
func nssTestStart() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }

// nssTestConfig is LDAP and NSS enabled with the shipped NSS defaults, the
// base DN dc=test and no ID attribute.
func nssTestConfig() *config.Config {
	c := ldapTestConfig(nil)
	c.LDAP.BaseDN = "dc=test"
	c.NSS = config.NSSConfig{
		Enabled:          true,
		UserFilter:       "(objectClass=posixAccount)",
		GroupFilter:      "(objectClass=posixGroup)",
		SyncInterval:     15 * time.Minute,
		FullSyncInterval: 24 * time.Hour,
		Attributes: config.NSSAttributes{
			Name: "uid", UID: "uidNumber", GID: "gidNumber", Home: "homeDirectory", Shell: "loginShell",
			GroupName: "cn", GroupGID: "gidNumber", GroupMember: "memberUid",
			GecosAttributes: []string{"displayName", "cn"},
		},
		GecosTemplate:      "{{.DisplayName}}",
		AutoPrivateGroups:  config.AutoPrivateGroupsHybrid,
		TombstoneAfter:     168 * time.Hour,
		TombstoneRetention: 2160 * time.Hour,
		DefaultHome:        "/home/%u",
		DisabledShell:      "/usr/sbin/nologin",
		MaxGroupMembers:    2000,
		RateLimit:          20,
		RateBurst:          200,
		KeyDefaultLifetime: 8760 * time.Hour,
	}
	return c
}

// nssHarness is one service over a fresh database, a fake directory and a
// test clock, with an audit recorder attached.
type nssHarness struct {
	svc   *NSSDirectoryService
	db    *gorm.DB
	dir   *nssFakeDirectory
	clock *nssClock
}

// newNSSHarness builds the service for cfg (nssTestConfig when nil).
func newNSSHarness(t *testing.T, cfg *config.Config) *nssHarness {
	t.Helper()
	if cfg == nil {
		cfg = nssTestConfig()
	}
	db := testdb.New(t)
	svc, err := NewNSSDirectoryService(cfg, db)
	if err != nil {
		t.Fatalf("NewNSSDirectoryService() error = %v", err)
	}
	dir := &nssFakeDirectory{}
	clock := &nssClock{t: nssTestStart()}
	svc.dial = dir.dialer()
	svc.now = clock.now
	svc.SetAuditor(NewAuditService(cfg, db))
	return &nssHarness{svc: svc, db: db, dir: dir, clock: clock}
}

// nssAccountEntry builds a posixAccount entry under ou=people,dc=test
// unless dn names another place. extra adds or overrides attributes.
func nssAccountEntry(dn, name string, uid, gid uint32, extra map[string][]string) *ldap.Entry {
	if dn == "" {
		dn = "uid=" + name + ",ou=people,dc=test"
	}
	attrs := map[string][]string{
		"objectClass":   {"top", "posixAccount", "inetOrgPerson"},
		"uid":           {name},
		"uidNumber":     {strconv.FormatUint(uint64(uid), 10)},
		"gidNumber":     {strconv.FormatUint(uint64(gid), 10)},
		"cn":            {name},
		"homeDirectory": {"/home/" + name},
		"loginShell":    {"/bin/bash"},
	}
	for k, v := range extra {
		if v == nil {
			delete(attrs, k)
			continue
		}
		attrs[k] = v
	}
	return nssEntry(dn, attrs)
}

// nssEntry builds an entry whose attributes carry their raw bytes too, as
// the directory's do, so directoryIDValue can read an ID attribute.
func nssEntry(dn string, attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{DN: dn}
	for name, values := range attrs {
		raw := make([][]byte, 0, len(values))
		for _, v := range values {
			raw = append(raw, []byte(v))
		}
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: name, Values: values, ByteValues: raw})
	}
	return e
}

// nssGroupEntry builds a posixGroup entry under ou=groups,dc=test with
// memberUid members.
func nssGroupEntry(name string, gid uint32, members ...string) *ldap.Entry {
	attrs := map[string][]string{
		"objectClass": {"top", "posixGroup"},
		"cn":          {name},
		"gidNumber":   {strconv.FormatUint(uint64(gid), 10)},
	}
	if len(members) > 0 {
		attrs["memberUid"] = members
	}
	return nssEntry("cn="+name+",ou=groups,dc=test", attrs)
}

// accountByName reads the stored account named name, failing the test when
// there is none.
func (h *nssHarness) accountByName(t *testing.T, name string) model.PosixUser {
	t.Helper()
	var row model.PosixUser
	if err := h.db.Where("name = ?", name).First(&row).Error; err != nil {
		t.Fatalf("read account %q: %v", name, err)
	}
	return row
}

// groupByName reads the stored group named name.
func (h *nssHarness) groupByName(t *testing.T, name string) model.PosixGroup {
	t.Helper()
	var row model.PosixGroup
	if err := h.db.Where("name = ?", name).First(&row).Error; err != nil {
		t.Fatalf("read group %q: %v", name, err)
	}
	return row
}

// count returns how many rows of m match where.
func (h *nssHarness) count(t *testing.T, m any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	q := h.db.Model(m)
	if where != "" {
		q = q.Where(where, args...)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// audits returns how many audit rows carry action.
func (h *nssHarness) audits(t *testing.T, action AuditAction) int64 {
	t.Helper()
	return h.count(t, &model.AuditEvent{}, "action = ?", string(action))
}

// members returns the uids stored as members of gid, sorted.
func (h *nssHarness) members(t *testing.T, gid uint32) []uint32 {
	t.Helper()
	var rows []model.PosixGroupMember
	if err := h.db.Where("gid = ?", gid).Order("uid").Find(&rows).Error; err != nil {
		t.Fatalf("read members of %d: %v", gid, err)
	}
	out := make([]uint32, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.UID)
	}
	return out
}

// fullSync runs a full sync and fails the test on error.
func (h *nssHarness) fullSync(t *testing.T) *NSSSyncResult {
	t.Helper()
	result, err := h.svc.FullSync(t.Context())
	if err != nil {
		t.Fatalf("FullSync() error = %v", err)
	}
	return result
}
