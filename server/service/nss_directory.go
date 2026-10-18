package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	// text/template for the gecos field; see nss_entry.go for why not
	// html/template.
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"text/template"

	"github.com/go-ldap/ldap/v3"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
)

// NSS directory tuning. Fixed rather than configurable: none of them is a
// policy an operator chooses.
const (
	// nssNegativeCacheTTL is how long an on-demand read that found nothing
	// servable is remembered, so a host asking again for a name the
	// directory does not have is not another directory search.
	nssNegativeCacheTTL = 60 * time.Second

	// nssNegativeCacheMax caps the remembered misses on one instance. A
	// host enumerating made-up names would otherwise grow the map without
	// bound; at the cap, expired entries are swept and, if that frees
	// nothing, the map starts over, which costs only extra searches.
	nssNegativeCacheMax = 10000

	// nssPageSize is the page size of the sync's paged searches (RFC
	// 2696). Small enough for any directory's paged-results limit, large
	// enough that a big directory is not thousands of round trips.
	nssPageSize = 500

	// nssLookupSizeLimit is the size limit of an on-demand read by name or
	// number. One entry answers it; a second is a collision, and either
	// way nothing more is worth fetching.
	nssLookupSizeLimit = 2

	// nssIncrementalOverlap is how far before the last successful run's
	// start the incremental sync looks back, so an entry modified while
	// that run was reading, or stamped by a directory whose clock runs
	// behind the server's, is read again rather than missed.
	nssIncrementalOverlap = 5 * time.Minute

	// nssAlertWindow is how often one alert may be raised for one value,
	// row or person, across every instance.
	nssAlertWindow = 24 * time.Hour

	// nssBatchSize bounds the rows one SQL statement names or inserts.
	nssBatchSize = 500
)

// ErrNSSSyncInProgress is returned when an NSS sync is already running on
// this instance. One run at a time per instance, so a manual run cannot
// stack on a scheduled one over the same entries.
var ErrNSSSyncInProgress = errors.New("an NSS directory sync is already running on this instance")

// NSSDirectoryService keeps the POSIX accounts and groups the NSS lookups
// serve in step with the directory: a paged full sync of nss.user_filter
// and nss.group_filter, an incremental sync between full ones, on-demand
// reads for an entry created since the last sync, the link from an account
// to the person who logs in with it, the tombstone purge, and the admin
// actions on pending id changes, collisions and tombstones. See
// docs/proposals/nss-directory/1-server-api.md.
//
// A nil *NSSDirectoryService means NSS is disabled; every method is
// nil-receiver safe and does nothing, the way LDAPService is.
//
// Nothing here fails a lookup or a login: a directory that cannot be
// reached is an error the caller logs, and the stored rows keep being
// served as they are.
type NSSDirectoryService struct {
	config *config.Config
	db     *gorm.DB
	dial   ldapDialer
	log    *slog.Logger
	// now is the clock, replaced in tests.
	now func() time.Time

	auditor *AuditService

	// gecos is nss.gecos_template, parsed once at construction.
	gecos *template.Template

	// accountAttrs, groupAttrs and groupOwnAttrs are the attributes each
	// kind of read requests: everything an account is written from;
	// everything a group is written from, its members included; and a
	// group without its members, for the membership search, which only
	// needs to know the group.
	accountAttrs  []string
	groupAttrs    []string
	groupOwnAttrs []string

	// syncing is the single-flight guard on the sync runs, full and
	// incremental together.
	syncing atomic.Bool

	// baselineMu guards baseline, the start of the last successful sync on
	// this instance, which the next incremental run reads changes since.
	// Zero until one succeeds, which makes the first incremental run a
	// full one.
	baselineMu sync.Mutex
	baseline   time.Time

	// reads collapses concurrent on-demand reads of one name or number into
	// one directory search.
	reads singleflight.Group

	// negativeMu guards negative, the on-demand reads that found nothing
	// servable, by cache key, with when each stops counting.
	negativeMu sync.Mutex
	negative   map[string]nssNegative
}

// nssNegative is one remembered on-demand read: what it reported, and
// until when the answer stands.
type nssNegative struct {
	found   bool
	expires time.Time
}

// NewNSSDirectoryService checks the parts of the NSS configuration that
// need the LDAP library, so a bad filter or gecos template fails startup
// rather than every sync. Returns nil when NSS is disabled, which every
// caller treats as "no NSS directory".
func NewNSSDirectoryService(c *config.Config, db *gorm.DB) (*NSSDirectoryService, error) {
	if !c.NSS.Enabled {
		return nil, nil //nolint:nilnil // a disabled directory is not an error; callers check for nil.
	}

	filters := []struct {
		key    string
		filter string
	}{
		{"nss.user_filter", c.NSS.UserFilter},
		{"nss.group_filter", c.NSS.GroupFilter},
		{"nss.disabled_filter", c.NSS.DisabledFilter},
	}
	for _, f := range filters {
		if f.filter == "" {
			// Only disabled_filter may be empty; config validation has
			// already required the other two.
			continue
		}
		if _, err := ldap.CompileFilter(f.filter); err != nil {
			return nil, fmt.Errorf("%s %q is not a valid LDAP filter: %w", f.key, f.filter, err)
		}
	}

	gecos, err := parseGecosTemplate(c.NSS.GecosTemplate)
	if err != nil {
		return nil, err
	}

	attrs := c.NSS.Attributes
	s := &NSSDirectoryService{
		config:        c,
		db:            db,
		dial:          dialLDAP,
		log:           logging.Tagged(logging.TagLDAP),
		now:           time.Now,
		gecos:         gecos,
		accountAttrs:  nssAttrList(c.LDAP.IDAttribute, append([]string{attrs.Name, attrs.UID, attrs.GID, attrs.Home, attrs.Shell}, attrs.GecosAttributes...)...),
		groupAttrs:    nssAttrList(c.LDAP.IDAttribute, attrs.GroupName, attrs.GroupGID, attrs.GroupMember),
		groupOwnAttrs: nssAttrList(c.LDAP.IDAttribute, attrs.GroupName, attrs.GroupGID),
		negative:      map[string]nssNegative{},
	}
	return s, nil
}

// nssAttrList is attrs with the re-anchoring ID attribute added when one is
// configured, without duplicates.
func nssAttrList(idAttr string, attrs ...string) []string {
	if idAttr != "" {
		attrs = append(attrs, idAttr)
	}
	return dedupe(attrs)
}

// SetAuditor attaches the audit recorder for the alerts, the purge and the
// admin actions.
func (s *NSSDirectoryService) SetAuditor(a *AuditService) {
	if s != nil {
		s.auditor = a
	}
}

// RefreshUserByName reads the account named name from the directory, for a
// name a host asked for that the database cannot serve, and stores it as
// the sync would. It reports whether the directory has an account by
// exactly that name; the caller then serves from the database. An error
// means the directory could not be read and the database is all there is.
func (s *NSSDirectoryService) RefreshUserByName(ctx context.Context, name string) (bool, error) {
	if s == nil {
		return false, nil
	}
	filter := s.andFilter(s.config.NSS.UserFilter, s.config.NSS.Attributes.Name, name)
	return s.cachedRead(ctx, "user/name/"+name, func(ctx context.Context) (bool, bool, error) {
		return s.readAccountsOnDemand(ctx, nil, filter, nssKinds{name: true}, nssViaOnDemand, func(a *nssAccount) bool { return a.Name == name })
	})
}

// RefreshUserByUID is RefreshUserByName for a uid.
func (s *NSSDirectoryService) RefreshUserByUID(ctx context.Context, uid uint32) (bool, error) {
	if s == nil {
		return false, nil
	}
	value := strconv.FormatUint(uint64(uid), 10)
	filter := s.andFilter(s.config.NSS.UserFilter, s.config.NSS.Attributes.UID, value)
	return s.cachedRead(ctx, "user/uid/"+value, func(ctx context.Context) (bool, bool, error) {
		return s.readAccountsOnDemand(ctx, nil, filter, nssKinds{number: true}, nssViaOnDemand, func(a *nssAccount) bool { return a.UID == uid })
	})
}

// RefreshGroupByName reads the group named name from the directory and
// stores it, with every member that resolves to a stored account, as the
// sync would. It reports and fails as RefreshUserByName does.
func (s *NSSDirectoryService) RefreshGroupByName(ctx context.Context, name string) (bool, error) {
	if s == nil {
		return false, nil
	}
	filter := s.andFilter(s.config.NSS.GroupFilter, s.config.NSS.Attributes.GroupName, name)
	return s.cachedRead(ctx, "group/name/"+name, func(ctx context.Context) (bool, bool, error) {
		return s.readGroupsOnDemand(ctx, filter, nssKinds{name: true}, func(g *nssGroup) bool { return g.Name == name })
	})
}

// RefreshGroupByGID is RefreshGroupByName for a gid.
func (s *NSSDirectoryService) RefreshGroupByGID(ctx context.Context, gid uint32) (bool, error) {
	if s == nil {
		return false, nil
	}
	value := strconv.FormatUint(uint64(gid), 10)
	filter := s.andFilter(s.config.NSS.GroupFilter, s.config.NSS.Attributes.GroupGID, value)
	return s.cachedRead(ctx, "group/gid/"+value, func(ctx context.Context) (bool, bool, error) {
		return s.readGroupsOnDemand(ctx, filter, nssKinds{number: true}, func(g *nssGroup) bool { return g.GID == gid })
	})
}

// andFilter is base ANDed with an equality on attr, the value escaped per
// RFC 4515 so a name a host sent cannot widen the search.
func (s *NSSDirectoryService) andFilter(base, attr, value string) string {
	return fmt.Sprintf("(&%s(%s=%s))", base, ldap.EscapeFilter(attr), ldap.EscapeFilter(value))
}

// cachedRead runs one on-demand read through the negative cache and the
// single-flight group. read reports whether the directory has the entry
// and whether a host can now be served it; an answer that left nothing
// servable (a miss, or an entry withheld by a collision) is remembered for
// nssNegativeCacheTTL, so asking again inside that time is not another
// search. Errors are never remembered.
func (s *NSSDirectoryService) cachedRead(ctx context.Context, key string, read func(context.Context) (found, servable bool, err error)) (bool, error) {
	if found, ok := s.negativeLookup(key); ok {
		return found, nil
	}

	type outcome struct{ found, servable bool }
	v, err, _ := s.reads.Do(key, func() (any, error) {
		// The search is shared by every caller waiting on this key, so it
		// must not end with whichever of them gives up first.
		found, servable, err := read(context.WithoutCancel(ctx))
		return outcome{found: found, servable: servable}, err
	})
	if err != nil {
		return false, err
	}
	out, ok := v.(outcome)
	if !ok {
		// not covered: the function above only ever returns an outcome.
		return false, fmt.Errorf("nss directory read returned %T", v)
	}
	if !out.servable {
		s.negativeStore(key, out.found)
	}
	return out.found, nil
}

// negativeLookup returns the remembered answer for key while it stands.
func (s *NSSDirectoryService) negativeLookup(key string) (bool, bool) {
	s.negativeMu.Lock()
	defer s.negativeMu.Unlock()

	entry, ok := s.negative[key]
	if !ok {
		return false, false
	}
	if !s.now().Before(entry.expires) {
		delete(s.negative, key)
		return false, false
	}
	return entry.found, true
}

// negativeStore remembers an answer for key, keeping the map under
// nssNegativeCacheMax.
func (s *NSSDirectoryService) negativeStore(key string, found bool) {
	s.negativeMu.Lock()
	defer s.negativeMu.Unlock()

	now := s.now()
	if len(s.negative) >= nssNegativeCacheMax {
		for k, entry := range s.negative {
			if !now.Before(entry.expires) {
				delete(s.negative, k)
			}
		}
		if len(s.negative) >= nssNegativeCacheMax {
			s.negative = map[string]nssNegative{}
		}
	}
	s.negative[key] = nssNegative{found: found, expires: now.Add(nssNegativeCacheTTL)}
}

// withConn runs fn on conn when the caller already holds one (the login
// path), else on a fresh connection that is closed afterwards.
func (s *NSSDirectoryService) withConn(ctx context.Context, conn ldapConn, fn func(ldapConn) error) error {
	if conn != nil {
		return fn(conn)
	}
	fresh, err := s.dial(&s.config.LDAP)
	if err != nil {
		return err
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the directory connection", "error", err)
		}
	}()
	return fn(fresh)
}

// search runs one bounded subtree search under ldap.base_dn. A size-limit
// hit returns the entries that arrived, which is what a lookup that asked
// for at most two wants to see.
func (s *NSSDirectoryService) search(conn ldapConn, filter string, attrs []string, sizeLimit int) ([]*ldap.Entry, error) {
	req := ldap.NewSearchRequest(
		s.config.LDAP.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		sizeLimit, int(s.config.LDAP.Timeout.Seconds()), false,
		filter, attrs, nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) && result != nil {
			return result.Entries, nil
		}
		return nil, fmt.Errorf("directory search %s failed: %w", filter, err)
	}
	return result.Entries, nil
}

// pagedSearch enumerates filter under ldap.base_dn in pages of
// nssPageSize. Unlike search, a size-limit hit is a failure: an
// enumeration that stopped short would read as entries that have gone.
func (s *NSSDirectoryService) pagedSearch(conn ldapConn, filter string, attrs []string) ([]*ldap.Entry, error) {
	req := ldap.NewSearchRequest(
		s.config.LDAP.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, int(s.config.LDAP.Timeout.Seconds()), false,
		filter, attrs, nil,
	)
	result, err := conn.SearchWithPaging(req, nssPageSize)
	if err != nil {
		return nil, fmt.Errorf("paged directory search %s failed: %w", filter, err)
	}
	return result.Entries, nil
}

// readDN reads the entry at dn if it matches filter: a base-scope search,
// so nil (with no error) means the entry does not exist or does not match.
func (s *NSSDirectoryService) readDN(conn ldapConn, dn, filter string, attrs []string) (*ldap.Entry, error) {
	req := ldap.NewSearchRequest(
		dn,
		ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, int(s.config.LDAP.Timeout.Seconds()), false,
		filter, attrs, nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return nil, nil
		}
		return nil, fmt.Errorf("directory read of %s failed: %w", dn, err)
	}
	if len(result.Entries) == 0 {
		return nil, nil
	}
	return result.Entries[0], nil
}

// dirDisabled tests the entry at dn against nss.disabled_filter. An empty
// filter means the directory never disables an account.
func (s *NSSDirectoryService) dirDisabled(conn ldapConn, dn string) (bool, error) {
	if s.config.NSS.DisabledFilter == "" {
		return false, nil
	}
	// 1.1 asks for no attributes: only whether the entry matches matters.
	entry, err := s.readDN(conn, dn, s.config.NSS.DisabledFilter, []string{"1.1"})
	if err != nil {
		return false, err
	}
	return entry != nil, nil
}

// nssOnDemandAccount is one account an on-demand read found, with what the
// read learned about it before anything is written.
type nssOnDemandAccount struct {
	account  *nssAccount
	disabled bool
	groups   []*nssGroup
	// gone is the evidence for re-anchoring a renamed group among groups.
	gone *nssGroupGone
}

// readAccountsOnDemand is one on-demand account read: one search for
// filter with size limit 2, where a second entry is a collision; the
// disabled filter on each entry found; and one membership search per entry
// for the groups that list it, so its groups are right before the next
// sync. Everything is read before anything is written, so a read that
// fails halfway writes nothing. Then each account is stored as the sync
// would store it, with the other as its contender, each group found is
// stored without its members, and the account's membership rows are
// replaced by exactly the groups found.
//
// conn is the caller's connection (the login path), or nil to dial one.
// queried is the value the filter searched by: when the search came back
// with fewer entries than its size limit it saw every entry carrying it,
// so a collision over it that the search no longer shows can be cleared.
// match picks the entry the caller asked for; found reports whether one
// did, and servable whether that entry is now stored and not withheld.
// via is the path for the audit events: nssViaOnDemand or nssViaLogin.
func (s *NSSDirectoryService) readAccountsOnDemand(ctx context.Context, conn ldapConn, filter string, queried nssKinds, via string, match func(*nssAccount) bool) (found, servable bool, err error) {
	var reads []nssOnDemandAccount
	var complete nssKinds
	err = s.withConn(ctx, conn, func(conn ldapConn) error {
		reads, complete, err = s.searchAccountsOnDemand(ctx, conn, filter, queried)
		return err
	})
	if err != nil {
		return false, false, err
	}

	for i, read := range reads {
		var contenders []*nssAccount
		for j := range reads {
			if j != i {
				contenders = append(contenders, reads[j].account)
			}
		}
		stored, err := s.storeAccount(ctx, read.account, read.disabled, contenders, complete, nssWrite{via: via})
		if err != nil {
			return false, false, err
		}
		if stored.row != nil {
			if err := s.storeMembershipsOf(ctx, stored.row, read.groups, read.gone, via); err != nil {
				return false, false, err
			}
		}
		if match(read.account) {
			found = true
			servable = stored.row != nil && stored.row.WithheldAt == nil
		}
	}
	return found, servable, nil
}

// searchAccountsOnDemand is readAccountsOnDemand's read half: the search,
// then the disabled filter, the membership search and the rename check of
// its groups (probeGroupsGone) for each entry it found. complete is queried when the search saw every entry carrying the
// value, and nothing otherwise.
func (s *NSSDirectoryService) searchAccountsOnDemand(ctx context.Context, conn ldapConn, filter string, queried nssKinds) ([]nssOnDemandAccount, nssKinds, error) {
	entries, err := s.search(conn, filter, s.accountAttrs, nssLookupSizeLimit)
	if err != nil {
		return nil, nssKinds{}, err
	}
	complete := nssKinds{}
	if len(entries) < nssLookupSizeLimit {
		complete = queried
	}
	reads := make([]nssOnDemandAccount, 0, len(entries))
	for _, e := range entries {
		account, err := s.parseAccount(e)
		if err != nil {
			s.log.WarnContext(ctx, "skipped a directory account entry", "dn", e.DN, "error", err)
			continue
		}
		disabled, err := s.dirDisabled(conn, account.DN)
		if err != nil {
			return nil, nssKinds{}, err
		}
		groups, err := s.membershipGroups(ctx, conn, account)
		if err != nil {
			return nil, nssKinds{}, err
		}
		reads = append(reads, nssOnDemandAccount{
			account: account, disabled: disabled, groups: groups,
			gone: s.probeGroupsGone(ctx, conn, groups),
		})
	}
	return reads, complete, nil
}

// membershipGroups finds the groups that list account as a member: one
// search of nss.group_filter ANDed with the member attribute equal to the
// account's name, or to its DN when the attribute holds DNs. Bounded by
// ldap.limits.max_entries_per_search like any per-person search; a person
// in more groups than that is logged.
func (s *NSSDirectoryService) membershipGroups(ctx context.Context, conn ldapConn, account *nssAccount) ([]*nssGroup, error) {
	value := account.Name
	if nssMembersAreDNs(s.config.NSS.Attributes.GroupMember) {
		value = account.DN
	}
	filter := s.andFilter(s.config.NSS.GroupFilter, s.config.NSS.Attributes.GroupMember, value)
	limit := s.config.LDAP.Limits.MaxEntriesPerSearch
	entries, err := s.search(conn, filter, s.groupOwnAttrs, limit)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) >= limit {
		s.log.WarnContext(ctx, "an account's membership search reached ldap.limits.max_entries_per_search; the next full sync completes it",
			"dn", account.DN, "limit", limit)
	}
	groups := make([]*nssGroup, 0, len(entries))
	for _, e := range entries {
		group, err := s.parseGroup(e)
		if err != nil {
			s.log.WarnContext(ctx, "skipped a directory group entry", "dn", e.DN, "error", err)
			continue
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// readGroupsOnDemand is one on-demand group read: one search for filter
// with size limit 2, where a second entry is a collision, then each group
// stored with every member that resolves to a stored account, after the
// rename check (probeGroupsGone) on the same connection. queried, match
// and the results are as readAccountsOnDemand's.
func (s *NSSDirectoryService) readGroupsOnDemand(ctx context.Context, filter string, queried nssKinds, match func(*nssGroup) bool) (found, servable bool, err error) {
	var groups []*nssGroup
	var complete nssKinds
	var gone *nssGroupGone
	err = s.withConn(ctx, nil, func(conn ldapConn) error {
		groups, complete, err = s.searchGroupsOnDemand(ctx, conn, filter, queried)
		if err == nil {
			gone = s.probeGroupsGone(ctx, conn, groups)
		}
		return err
	})
	if err != nil || len(groups) == 0 {
		return false, false, err
	}

	index, err := s.loadMemberIndex(ctx)
	if err != nil {
		return false, false, err
	}
	for i, group := range groups {
		var contenders []*nssGroup
		for j := range groups {
			if j != i {
				contenders = append(contenders, groups[j])
			}
		}
		stored, err := s.storeGroup(ctx, group, contenders, complete, index, gone, nssWrite{via: nssViaOnDemand})
		if err != nil {
			return false, false, err
		}
		if match(group) {
			found = true
			servable = stored.row != nil && stored.row.WithheldAt == nil
		}
	}
	return found, servable, nil
}

// searchGroupsOnDemand is readGroupsOnDemand's read half.
func (s *NSSDirectoryService) searchGroupsOnDemand(ctx context.Context, conn ldapConn, filter string, queried nssKinds) ([]*nssGroup, nssKinds, error) {
	entries, err := s.search(conn, filter, s.groupAttrs, nssLookupSizeLimit)
	if err != nil {
		return nil, nssKinds{}, err
	}
	complete := nssKinds{}
	if len(entries) < nssLookupSizeLimit {
		complete = queried
	}
	groups := make([]*nssGroup, 0, len(entries))
	for _, e := range entries {
		group, err := s.parseGroup(e)
		if err != nil {
			s.log.WarnContext(ctx, "skipped a directory group entry", "dn", e.DN, "error", err)
			continue
		}
		groups = append(groups, group)
	}
	return groups, complete, nil
}

// storeMembershipsOf stores each group an account's membership search
// found (its own attributes only: its members are the sync's to set) and
// replaces the account's membership rows with exactly those groups. gone
// is the search's evidence for re-anchoring a renamed group, and via the
// path for the audit: each group the account joined or left records
// nss.group_members_changed. Only the difference is written, so a
// membership that stays keeps its added_at and a new one gets the current
// time.
func (s *NSSDirectoryService) storeMembershipsOf(ctx context.Context, account *model.PosixUser, groups []*nssGroup, gone *nssGroupGone, via string) error {
	gids := make([]uint32, 0, len(groups))
	for _, group := range groups {
		stored, err := s.storeGroup(ctx, group, nil, nssKinds{}, nil, gone, nssWrite{via: via})
		if err != nil {
			return err
		}
		if stored.row != nil && stored.row.DeletedAt == nil {
			gids = append(gids, stored.row.GID)
		}
	}
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		before, err := readMembers(tx, "uid = ?", account.UID)
		if err != nil {
			return err
		}
		after := nssMembers{}
		for _, gid := range gids {
			after.add(gid, account.UID)
		}
		deltas := memberDeltas(before, after)
		if err := applyMemberDeltas(tx, deltas, memberAddedAt(nssWrite{via: via}, s.now().UTC())); err != nil {
			return err
		}
		return s.recordMemberChanges(tx, &batch, deltas, nssMemberChange{
			via: via, userNames: map[uint32]string{account.UID: account.Name}, userIDs: map[uint32]string{account.UID: account.ID},
		})
	})
	if err != nil {
		return err
	}
	s.flush(&batch)
	return nil
}
