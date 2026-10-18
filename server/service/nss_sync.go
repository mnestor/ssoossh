package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// nssUniquenessLimit is the size limit of the incremental sync's
// uniqueness search: the entry itself and up to three others carrying its
// number or name. Reaching it means the search may not have seen every
// such entry, so the store keeps the collisions it cannot rule out.
const nssUniquenessLimit = 4

// NSSSyncCounts is what one sync run did to the accounts or the groups.
// The JSON names are webtypes.NSSSyncCountsResponse's, so the audit event
// that records a run (see nssSyncAudit) reads like the API response.
type NSSSyncCounts struct {
	// Seen is the entries the directory returned.
	Seen int `json:"seen"`
	// Skipped is the entries rejected by parsing (a name that fails the
	// POSIX rules, a uid that is not a number, a relative home), each
	// logged with its DN.
	Skipped int `json:"skipped"`
	// Created and Updated are the rows written.
	Created int `json:"created"`
	Updated int `json:"updated"`
	// Refused is the new entries not stored because another row, a
	// tombstone included, holds their number or name.
	Refused int `json:"refused"`
	// Failed is the entries that could not be checked or written; each is
	// logged and left as it was.
	Failed int `json:"failed"`
	// Missing and Tombstoned are the rows a full run marked missing for the
	// first time and the rows it tombstoned.
	Missing    int `json:"missing"`
	Tombstoned int `json:"tombstoned"`
}

// NSSSyncResult reports one sync run.
type NSSSyncResult struct {
	// Full is true for a full run, including an incremental one that ran
	// as full (the first on this instance, or one the directory refused
	// the modifyTimestamp filter for).
	Full       bool
	StartedAt  time.Time
	FinishedAt time.Time
	Accounts   NSSSyncCounts
	Groups     NSSSyncCounts
}

// FullSync reads every account and group the NSS filters select and
// brings the stored rows in line, then marks what it did not see as
// missing (and, after nss.tombstone_after, tombstoned) and clears the
// collisions it no longer saw. Single-flight per instance with the
// incremental sync: ErrNSSSyncInProgress when either is running.
func (s *NSSDirectoryService) FullSync(ctx context.Context) (*NSSSyncResult, error) {
	return s.sync(ctx, true)
}

// IncrementalSync reads only the entries modified since five minutes
// before the last successful run on this instance began, and stores them
// as the full sync would. It never marks anything missing. With no earlier
// run, or when the directory refuses the modifyTimestamp filter, it runs as
// a full sync instead.
func (s *NSSDirectoryService) IncrementalSync(ctx context.Context) (*NSSSyncResult, error) {
	return s.sync(ctx, false)
}

// SyncRunning reports whether a sync is running on this instance.
func (s *NSSDirectoryService) SyncRunning() bool {
	return s != nil && s.syncing.Load()
}

// lastBaseline is the start of the last successful run.
func (s *NSSDirectoryService) lastBaseline() time.Time {
	s.baselineMu.Lock()
	defer s.baselineMu.Unlock()
	return s.baseline
}

// setBaseline records a successful run's start.
func (s *NSSDirectoryService) setBaseline(t time.Time) {
	s.baselineMu.Lock()
	defer s.baselineMu.Unlock()
	s.baseline = t
}

// sync is one run. A dial or search failure counts nothing as missing and
// leaves every row as it was; an entry that cannot be written is logged
// and counted, and the run goes on.
func (s *NSSDirectoryService) sync(ctx context.Context, full bool) (*NSSSyncResult, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // a disabled directory has no run to report.
	}
	if !s.syncing.CompareAndSwap(false, true) {
		return nil, ErrNSSSyncInProgress
	}
	defer s.syncing.Store(false)

	// Truncated so a row written in the same microsecond as the start, as
	// Postgres stores it, never compares as older than the start.
	start := s.now().UTC().Truncate(time.Microsecond)
	since := time.Time{}
	if !full {
		since = s.lastBaseline()
		if since.IsZero() {
			s.log.InfoContext(ctx, "no earlier NSS sync on this instance; running the incremental sync as a full one")
			full = true
		}
	}
	result := &NSSSyncResult{Full: full, StartedAt: start}

	conn, err := s.dial(&s.config.LDAP)
	if err != nil {
		s.log.ErrorContext(ctx, "the NSS sync could not reach the directory; nothing was counted as missing", "error", err)
		return result, fmt.Errorf("nss sync could not connect: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the directory connection", "error", err)
		}
	}()

	if err := s.syncAccounts(ctx, conn, result, since); err != nil {
		return result, err
	}
	if err := s.syncGroups(ctx, conn, result, since); err != nil {
		return result, err
	}

	s.setBaseline(start)
	result.FinishedAt = s.now().UTC()
	s.log.InfoContext(ctx, "nss directory sync completed",
		"full", result.Full,
		"accounts_seen", result.Accounts.Seen, "accounts_created", result.Accounts.Created,
		"accounts_updated", result.Accounts.Updated, "accounts_skipped", result.Accounts.Skipped,
		"accounts_refused", result.Accounts.Refused, "accounts_failed", result.Accounts.Failed,
		"accounts_missing", result.Accounts.Missing, "accounts_tombstoned", result.Accounts.Tombstoned,
		"groups_seen", result.Groups.Seen, "groups_created", result.Groups.Created,
		"groups_updated", result.Groups.Updated, "groups_skipped", result.Groups.Skipped,
		"groups_refused", result.Groups.Refused, "groups_failed", result.Groups.Failed,
		"groups_missing", result.Groups.Missing, "groups_tombstoned", result.Groups.Tombstoned)
	return result, nil
}

// modifiedSince is base ANDed with modifyTimestamp at or after t, in the
// GeneralizedTime form the directory compares.
func modifiedSince(base string, t time.Time) string {
	return fmt.Sprintf("(&%s(modifyTimestamp>=%s))", base, t.UTC().Format("20060102150405Z"))
}

// enumerate is the run's paged search of base: all of it on a full run,
// the entries modified since the baseline otherwise. A directory that
// refuses the incremental filter turns the run into a full one, retried on
// the same connection.
func (s *NSSDirectoryService) enumerate(ctx context.Context, conn ldapConn, result *NSSSyncResult, base string, attrs []string, since time.Time) ([]*ldap.Entry, error) {
	if !result.Full {
		entries, err := s.pagedSearch(conn, modifiedSince(base, since.Add(-nssIncrementalOverlap)), attrs)
		if err == nil {
			return entries, nil
		}
		s.log.WarnContext(ctx, "the directory refused the incremental NSS sync's modifyTimestamp filter; running a full sync",
			"error", err)
		result.Full = true
	}
	return s.pagedSearch(conn, base, attrs)
}

// syncAccounts is the run's account half.
func (s *NSSDirectoryService) syncAccounts(ctx context.Context, conn ldapConn, result *NSSSyncResult, since time.Time) error {
	counts := &result.Accounts
	importing, err := s.tableEmpty(ctx, accountTable())
	if err != nil {
		return err
	}
	entries, err := s.enumerate(ctx, conn, result, s.config.NSS.UserFilter, s.accountAttrs, since)
	if err != nil {
		return err
	}
	counts.Seen = len(entries)
	w := runWrite(result, importing)

	// One paged search for every disabled account on a full run, rather
	// than one read per account.
	var disabledDNs map[string]bool
	if result.Full && s.config.NSS.DisabledFilter != "" {
		disabled, err := s.pagedSearch(conn, fmt.Sprintf("(&%s%s)", s.config.NSS.UserFilter, s.config.NSS.DisabledFilter), []string{"1.1"})
		if err != nil {
			return err
		}
		disabledDNs = make(map[string]bool, len(disabled))
		for _, e := range disabled {
			disabledDNs[normalizeDN(e.DN)] = true
		}
	}

	accounts := make([]*nssAccount, 0, len(entries))
	for _, e := range entries {
		account, err := s.parseAccount(e)
		if err != nil {
			s.log.WarnContext(ctx, "skipped a directory account entry", "dn", e.DN, "error", err)
			counts.Skipped++
			continue
		}
		accounts = append(accounts, account)
	}
	duplicates := accountDuplicates(accounts)

	for _, account := range accounts {
		s.syncAccount(ctx, conn, result, account, duplicates(account), disabledDNs, w)
	}
	if w.importing {
		s.recordInitialImport(ctx, accountTable(), w.via, counts.Created)
	}

	if !result.Full || !s.completeRun(ctx, accountTable(), counts) {
		return nil
	}
	start := result.StartedAt
	if err := s.markMissing(ctx, accountTable(), start, counts); err != nil {
		return err
	}
	return s.sweepCollisions(ctx, accountTable(), start)
}

// runWrite is a sync run's path for the audit events, once its search has
// settled whether the run is full; importing is whether the table was
// empty when the run began.
func runWrite(result *NSSSyncResult, importing bool) nssWrite {
	via := nssViaIncrementalSync
	if result.Full {
		via = nssViaFullSync
	}
	return nssWrite{via: via, importing: importing}
}

// completeRun reports whether a full run may draw conclusions from what it
// did not see. An entry it read but could not write still has its old
// last_synced_at, and would read as missing, so a run with any such entry
// marks nothing missing and clears no collision for that table.
func (s *NSSDirectoryService) completeRun(ctx context.Context, t nssTable, counts *NSSSyncCounts) bool {
	if counts.Failed == 0 {
		return true
	}
	s.log.WarnContext(ctx, "the full NSS sync could not write every entry it read; nothing was marked missing",
		"table", t.table, "failed", counts.Failed)
	return false
}

// syncAccount stores one account a run read and counts the outcome. A
// full run already knows the entry's duplicates and whether it is
// disabled; an incremental run asks the directory for both.
func (s *NSSDirectoryService) syncAccount(ctx context.Context, conn ldapConn, result *NSSSyncResult, account *nssAccount, duplicates []*nssAccount, disabledDNs map[string]bool, w nssWrite) {
	counts := &result.Accounts
	contenders, complete := duplicates, nssAllKinds()
	disabled := disabledDNs[normalizeDN(account.DN)]
	if !result.Full {
		var err error
		if contenders, complete, err = s.accountContenders(conn, account); err == nil {
			disabled, err = s.dirDisabled(conn, account.DN)
		}
		if err != nil {
			s.log.WarnContext(ctx, "could not check a directory account; left as it was", "dn", account.DN, "error", err)
			counts.Failed++
			return
		}
	}

	stored, err := s.storeAccount(ctx, account, disabled, contenders, complete, w)
	switch {
	case err != nil:
		s.log.ErrorContext(ctx, "could not store a directory account", "dn", account.DN, "error", err)
		counts.Failed++
	case stored.row == nil:
		counts.Refused++
	case stored.created:
		counts.Created++
	default:
		counts.Updated++
	}
}

// syncGroups is the run's group half, after the accounts so every member
// the run stored resolves.
func (s *NSSDirectoryService) syncGroups(ctx context.Context, conn ldapConn, result *NSSSyncResult, since time.Time) error {
	counts := &result.Groups
	importing, err := s.tableEmpty(ctx, groupTable())
	if err != nil {
		return err
	}
	entries, err := s.enumerate(ctx, conn, result, s.config.NSS.GroupFilter, s.groupAttrs, since)
	if err != nil {
		return err
	}
	counts.Seen = len(entries)
	w := runWrite(result, importing)

	index, err := s.loadMemberIndex(ctx)
	if err != nil {
		return err
	}

	groups := make([]*nssGroup, 0, len(entries))
	for _, e := range entries {
		group, err := s.parseGroup(e)
		if err != nil {
			s.log.WarnContext(ctx, "skipped a directory group entry", "dn", e.DN, "error", err)
			counts.Skipped++
			continue
		}
		groups = append(groups, group)
	}
	duplicates := groupDuplicates(groups)
	// A full run enumerated every entry the filter selects, the skipped
	// ones included, so a stored group whose DN is not among them has gone.
	var enumerated *nssGroupGone
	if result.Full {
		enumerated = absentFrom(entries)
	}

	for _, group := range groups {
		contenders, complete, gone := duplicates(group), nssAllKinds(), enumerated
		if !result.Full {
			if contenders, complete, err = s.groupContenders(conn, group); err != nil {
				s.log.WarnContext(ctx, "could not check a directory group; left as it was", "dn", group.DN, "error", err)
				counts.Failed++
				continue
			}
			gone = s.probeGroupsGone(ctx, conn, []*nssGroup{group})
		}

		stored, err := s.storeGroup(ctx, group, contenders, complete, index, gone, w)
		switch {
		case err != nil:
			s.log.ErrorContext(ctx, "could not store a directory group", "dn", group.DN, "error", err)
			counts.Failed++
		case stored.row == nil:
			counts.Refused++
		case stored.created:
			counts.Created++
		default:
			counts.Updated++
		}
	}
	if w.importing {
		s.recordInitialImport(ctx, groupTable(), w.via, counts.Created)
	}
	if index.skipped > 0 {
		s.log.DebugContext(ctx, "group members that are not stored accounts were left out", "members", index.skipped)
	}

	if !result.Full || !s.completeRun(ctx, groupTable(), counts) {
		return nil
	}
	start := result.StartedAt
	if err := s.markMissing(ctx, groupTable(), start, counts); err != nil {
		return err
	}
	return s.sweepCollisions(ctx, groupTable(), start)
}

// absentFrom is a full run's evidence for re-anchoring renamed groups:
// entries is the complete enumeration, so a DN not among them is gone.
func absentFrom(entries []*ldap.Entry) *nssGroupGone {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[normalizeDN(e.DN)] = true
	}
	return &nssGroupGone{via: nssGoneByFullSync, gone: func(dn string) bool { return !seen[normalizeDN(dn)] }}
}

// probeGroupsGone is the same evidence outside a full run, gathered before
// anything is written. For each group no stored row anchors whose gid a
// row at another DN holds (renameCandidate), the holder's entry has gone
// when a search of nss.group_filter for the gid finds no entry but the
// group itself and a base-scope read of the holder's DN finds no group
// there. A directory or database error is never evidence: that group is
// stored as it would be without any, and the next read asks again.
func (s *NSSDirectoryService) probeGroupsGone(ctx context.Context, conn ldapConn, groups []*nssGroup) *nssGroupGone {
	gone := map[string]bool{}
	for _, g := range groups {
		holder, err := s.renameCandidate(s.db.WithContext(ctx), g)
		if err == nil && holder != nil {
			var absent bool
			if absent, err = s.groupEntryGone(conn, g, holder.DN); err == nil && absent {
				gone[normalizeDN(holder.DN)] = true
			}
		}
		if err != nil {
			s.log.WarnContext(ctx, "could not check whether a directory group was renamed; stored as a new entry",
				"dn", g.DN, "error", err)
		}
	}
	return &nssGroupGone{via: nssGoneByBaseRead, gone: func(dn string) bool { return gone[normalizeDN(dn)] }}
}

// groupEntryGone reports whether dn, a stored group's DN, no longer holds a
// group the filter selects, with g the only entry carrying g's gid.
func (s *NSSDirectoryService) groupEntryGone(conn ldapConn, g *nssGroup, dn string) (bool, error) {
	attrs := s.config.NSS.Attributes
	filter := s.andFilter(s.config.NSS.GroupFilter, attrs.GroupGID, strconv.FormatUint(uint64(g.GID), 10))
	// 1.1 asks for no attributes: only which entries carry the gid matters.
	carriers, err := s.search(conn, filter, []string{"1.1"}, nssLookupSizeLimit)
	if err != nil {
		return false, err
	}
	for _, e := range carriers {
		if normalizeDN(e.DN) != normalizeDN(g.DN) {
			return false, nil
		}
	}
	entry, err := s.readDN(conn, dn, s.config.NSS.GroupFilter, []string{"1.1"})
	if err != nil {
		return false, err
	}
	return entry == nil, nil
}

// accountDuplicates indexes a full run's accounts by uid and by name, and
// returns the function that gives each account the others that carry its
// uid or name: a full run sees every entry, so this is the whole picture.
func accountDuplicates(accounts []*nssAccount) func(*nssAccount) []*nssAccount {
	byUID := map[uint32][]*nssAccount{}
	byName := map[string][]*nssAccount{}
	for _, a := range accounts {
		byUID[a.UID] = append(byUID[a.UID], a)
		byName[a.Name] = append(byName[a.Name], a)
	}
	return func(a *nssAccount) []*nssAccount {
		return othersOf(a, byUID[a.UID], byName[a.Name])
	}
}

// groupDuplicates is accountDuplicates for groups, by gid and name.
func groupDuplicates(groups []*nssGroup) func(*nssGroup) []*nssGroup {
	byGID := map[uint32][]*nssGroup{}
	byName := map[string][]*nssGroup{}
	for _, g := range groups {
		byGID[g.GID] = append(byGID[g.GID], g)
		byName[g.Name] = append(byName[g.Name], g)
	}
	return func(g *nssGroup) []*nssGroup {
		return othersOf(g, byGID[g.GID], byName[g.Name])
	}
}

// othersOf is every entry in lists other than self, once each.
func othersOf[T comparable](self T, lists ...[]T) []T {
	var out []T
	for _, list := range lists {
		for _, e := range list {
			if e != self && !slices.Contains(out, e) {
				out = append(out, e)
			}
		}
	}
	return out
}

// accountContenders is the incremental run's uniqueness search for one
// account: every other entry of nss.user_filter carrying its uid or its
// name, and whether the search saw them all.
func (s *NSSDirectoryService) accountContenders(conn ldapConn, account *nssAccount) ([]*nssAccount, nssKinds, error) {
	attrs := s.config.NSS.Attributes
	filter := fmt.Sprintf("(&%s(|(%s=%d)(%s=%s)))", s.config.NSS.UserFilter,
		ldap.EscapeFilter(attrs.UID), account.UID, ldap.EscapeFilter(attrs.Name), ldap.EscapeFilter(account.Name))
	entries, err := s.search(conn, filter, s.accountAttrs, nssUniquenessLimit)
	if err != nil {
		return nil, nssKinds{}, err
	}
	var out []*nssAccount
	for _, e := range entries {
		if other, err := s.parseAccount(e); err == nil && other.DN != account.DN {
			out = append(out, other)
		}
	}
	complete := len(entries) < nssUniquenessLimit
	return out, nssKinds{number: complete, name: complete}, nil
}

// groupContenders is accountContenders for a group, by gid and name.
func (s *NSSDirectoryService) groupContenders(conn ldapConn, group *nssGroup) ([]*nssGroup, nssKinds, error) {
	attrs := s.config.NSS.Attributes
	filter := fmt.Sprintf("(&%s(|(%s=%d)(%s=%s)))", s.config.NSS.GroupFilter,
		ldap.EscapeFilter(attrs.GroupGID), group.GID, ldap.EscapeFilter(attrs.GroupName), ldap.EscapeFilter(group.Name))
	entries, err := s.search(conn, filter, s.groupOwnAttrs, nssUniquenessLimit)
	if err != nil {
		return nil, nssKinds{}, err
	}
	var out []*nssGroup
	for _, e := range entries {
		if other, err := s.parseGroup(e); err == nil && other.DN != group.DN {
			out = append(out, other)
		}
	}
	complete := len(entries) < nssUniquenessLimit
	return out, nssKinds{number: complete, name: complete}, nil
}

// markMissing is a full run's end for one table: every row the run did not
// read (last_synced_at older than its start) gets missing_since if it has
// none, and every such row missing for nss.tombstone_after is tombstoned.
// A tombstoned group loses its membership rows, so it grants nothing. Each
// row marked records nss.account_missing or nss.group_missing, each
// tombstone nss.account_tombstoned or nss.group_tombstoned, and a group's
// member wipe nss.group_members_changed.
func (s *NSSDirectoryService) markMissing(ctx context.Context, t nssTable, start time.Time, counts *NSSSyncCounts) error {
	now := s.now().UTC()
	missingAction, tombstonedAction := AuditNSSGroupMissing, AuditNSSGroupTombstoned
	if t == accountTable() {
		missingAction, tombstonedAction = AuditNSSAccountMissing, AuditNSSAccountTombstoned
	}

	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := t.lifecycleRows(tx, "last_synced_at < ? AND missing_since IS NULL", start)
		if err != nil {
			return err
		}
		for ids := range slices.Chunk(rowIDs(rows), nssBatchSize) {
			if err := tx.Table(t.table).Where("id IN ? AND missing_since IS NULL", ids).
				Updates(map[string]any{"missing_since": now, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		for _, row := range rows {
			if err := s.recordRowEvent(tx, &batch, t, missingAction, row.ref(), row.UserID, nssViaFullSync, nil); err != nil {
				return err
			}
		}
		counts.Missing = len(rows)
		return nil
	})
	if err != nil {
		return fmt.Errorf("mark missing %s rows: %w", t.label, err)
	}
	s.flush(&batch)

	due, err := t.lifecycleRows(s.db.WithContext(ctx),
		"last_synced_at < ? AND deleted_at IS NULL AND missing_since <= ?", start, now.Add(-s.config.NSS.TombstoneAfter))
	if err != nil {
		return fmt.Errorf("find %s rows to tombstone: %w", t.label, err)
	}
	for _, row := range due {
		if err := s.tombstone(ctx, t, row, tombstonedAction, now); err != nil {
			return fmt.Errorf("tombstone the %s %s: %w", t.label, row.DN, err)
		}
		s.log.InfoContext(ctx, "tombstoned an NSS "+t.label+" missing from the directory",
			"name", row.Name, t.numberColumn, strconv.FormatUint(uint64(row.Number), 10), "dn", row.DN)
	}
	counts.Tombstoned = len(due)
	return nil
}

// tombstone sets one missing row's deleted_at, wipes a group's members,
// and records both, in one transaction.
func (s *NSSDirectoryService) tombstone(ctx context.Context, t nssTable, row nssLifecycleRow, action AuditAction, now time.Time) error {
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table(t.table).Where("id = ? AND deleted_at IS NULL", row.ID).
			Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		detail := map[string]any{}
		if row.MissingSince != nil {
			detail["missing_since"] = *row.MissingSince
		}
		if err := s.recordRowEvent(tx, &batch, t, action, row.ref(), row.UserID, nssViaFullSync, detail); err != nil {
			return err
		}
		if t != groupTable() {
			return nil
		}
		before, err := readMembers(tx, "gid = ?", row.Number)
		if err != nil {
			return err
		}
		if err := tx.Where("gid = ?", row.Number).Delete(&model.PosixGroupMember{}).Error; err != nil {
			return err
		}
		return s.recordMemberChanges(tx, &batch, memberDeltas(before, nssMembers{}), nssMemberChange{
			via: nssViaTombstoned, groupNames: map[uint32]string{row.Number: row.Name},
		})
	})
	if err != nil {
		return err
	}
	s.flush(&batch)
	return nil
}

// nssLifecycleRow is a row as the missing and tombstone steps read it: the
// shared view, the linked person of an account, and since when it has
// been missing.
type nssLifecycleRow struct {
	ID           string     `gorm:"column:id"`
	DN           string     `gorm:"column:dn"`
	Name         string     `gorm:"column:name"`
	Number       uint32     `gorm:"column:number"`
	UserID       *string    `gorm:"column:user_id"`
	MissingSince *time.Time `gorm:"column:missing_since"`
}

// ref is the row's shared view.
func (r nssLifecycleRow) ref() nssRowRef {
	return nssRowRef{ID: r.ID, DN: r.DN, Name: r.Name, Number: r.Number}
}

// lifecycleRows reads the rows of t matching where.
func (t nssTable) lifecycleRows(tx *gorm.DB, where string, args ...any) ([]nssLifecycleRow, error) {
	cols := "id, dn, name, missing_since, " + t.numberColumn + " AS number"
	if t == accountTable() {
		cols += ", user_id"
	}
	var out []nssLifecycleRow
	if err := tx.Table(t.table).Select(cols).Where(where, args...).Order("created_at").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("read %s rows: %w", t.label, err)
	}
	return out, nil
}

// rowIDs is the ids of rows.
func rowIDs(rows []nssLifecycleRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// sweepCollisions is a full run's last step for one table: a collision the
// run did not see again (last_seen_at older than its start) is between
// entries that no longer clash, or one of which has gone, so it is deleted
// and the rows it withheld are served again unless another holds them.
func (s *NSSDirectoryService) sweepCollisions(ctx context.Context, t nssTable, start time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stale []model.NSSCollision
		if err := tx.Where("kind IN ? AND last_seen_at < ?", t.kinds(), start).Find(&stale).Error; err != nil {
			return fmt.Errorf("find resolved %s collisions: %w", t.label, err)
		}
		if len(stale) == 0 {
			return nil
		}
		if err := deleteCollisions(tx, stale); err != nil {
			return err
		}
		affected := map[string]bool{}
		markAffected(affected, stale)
		s.log.InfoContext(ctx, "cleared NSS collisions the full sync no longer saw",
			"table", t.table, "collisions", len(stale), "rows", slices.Sorted(maps.Keys(affected)))
		return s.recomputeWithheld(tx, t, affected, s.now().UTC())
	})
}
