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
	entries, err := s.enumerate(ctx, conn, result, s.config.NSS.UserFilter, s.accountAttrs, since)
	if err != nil {
		return err
	}
	counts.Seen = len(entries)

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
		s.syncAccount(ctx, conn, result, account, duplicates(account), disabledDNs)
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
func (s *NSSDirectoryService) syncAccount(ctx context.Context, conn ldapConn, result *NSSSyncResult, account *nssAccount, duplicates []*nssAccount, disabledDNs map[string]bool) {
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

	stored, err := s.storeAccount(ctx, account, disabled, contenders, complete)
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
	entries, err := s.enumerate(ctx, conn, result, s.config.NSS.GroupFilter, s.groupAttrs, since)
	if err != nil {
		return err
	}
	counts.Seen = len(entries)

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

	for _, group := range groups {
		contenders, complete := duplicates(group), nssAllKinds()
		if !result.Full {
			if contenders, complete, err = s.groupContenders(conn, group); err != nil {
				s.log.WarnContext(ctx, "could not check a directory group; left as it was", "dn", group.DN, "error", err)
				counts.Failed++
				continue
			}
		}

		stored, err := s.storeGroup(ctx, group, contenders, complete, index)
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
// A tombstoned group loses its membership rows, so it grants nothing.
func (s *NSSDirectoryService) markMissing(ctx context.Context, t nssTable, start time.Time, counts *NSSSyncCounts) error {
	now := s.now().UTC()
	db := s.db.WithContext(ctx)

	result := db.Table(t.table).
		Where("last_synced_at < ? AND missing_since IS NULL", start).
		Updates(map[string]any{"missing_since": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("mark missing %s rows: %w", t.label, result.Error)
	}
	counts.Missing = int(result.RowsAffected)

	var due []nssRowRef
	if err := db.Table(t.table).
		Select("id, directory_id, dn, name, "+t.numberColumn+" AS number").
		Where("last_synced_at < ? AND deleted_at IS NULL AND missing_since <= ?", start, now.Add(-s.config.NSS.TombstoneAfter)).
		Find(&due).Error; err != nil {
		return fmt.Errorf("find %s rows to tombstone: %w", t.label, err)
	}
	for _, row := range due {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table(t.table).Where("id = ? AND deleted_at IS NULL", row.ID).
				Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
			if t.numberColumn == "gid" {
				return tx.Where("gid = ?", row.Number).Delete(&model.PosixGroupMember{}).Error
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("tombstone the %s %s: %w", t.label, row.DN, err)
		}
		s.log.InfoContext(ctx, "tombstoned an NSS "+t.label+" missing from the directory",
			"name", row.Name, t.numberColumn, strconv.FormatUint(uint64(row.Number), 10), "dn", row.DN)
	}
	counts.Tombstoned = len(due)
	return nil
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
