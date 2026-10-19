package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// The admin key list's windows. See docs/proposals/nss-directory/
// 2-server-webui.md, "Fleet keys (admin)".
const (
	// NSSKeyUnusedWindow is how long a key must have gone without a
	// lookup for the unused filter to list it.
	NSSKeyUnusedWindow = 30 * 24 * time.Hour
	// NSSKeyHostsWindow is the window a row's host count covers.
	NSSKeyHostsWindow = 7 * 24 * time.Hour
	// nssKeyMatchedShown bounds the addresses an address search lists per
	// key, the most recently seen first; the total is reported beside them.
	nssKeyMatchedShown = 5
)

// NSSAdminKeyFilter is what the admin key list is narrowed by. Zero values
// filter nothing.
type NSSAdminKeyFilter struct {
	// Page carries the cursor, the page size and q: a substring of the
	// key's name or group or the creator's username or email, or the start
	// of the key's id, or, when it
	// reads as an IP address or a CIDR range, the address the key was used
	// from (ParseNSSKeyAddressSearch).
	Page paging.Params
	// Status is one derived status, empty for every one.
	Status model.NSSKeyStatus
	// ExpiringWithin lists only active keys whose expiry falls within this
	// long from now; zero filters nothing.
	ExpiringWithin time.Duration
	// Unused lists only keys with no lookup in NSSKeyUnusedWindow, never
	// used ones included.
	Unused bool
	// Alerts lists only keys a miss rate or key source alert fired for in
	// NSSKeyAlertLookback.
	Alerts bool
}

// NSSAdminKeyPage is one page of the admin key list, by name then id.
type NSSAdminKeyPage struct {
	Keys []NSSAdminKeyEntry
	// Total counts every key matching the filter, across all pages.
	Total int64
	// NextCursor is the after for the following page, nil on the last.
	NextCursor *string
	// Address is how q was read when it is an address search, nil for a
	// text search.
	Address *NSSKeyAddressSearch
	// UsageRetention is nss.usage_retention, the window an address search
	// covers; zero keeps every row.
	UsageRetention time.Duration
}

// NSSAdminKeyEntry is one key on the admin list: the row without its secret
// hashes, its creator as the users table has them, and how hosts use it.
type NSSAdminKeyEntry struct {
	// Key carries no SecretHash or PreviousSecretHash: they are never read.
	Key    model.NSSKey
	Status model.NSSKeyStatus

	// CreatorFound is false when no users row has Key.CreatedBy any more;
	// the username and email are then empty.
	CreatorFound    bool
	CreatorUsername string
	CreatorEmail    string

	// Hosts is the number of distinct source addresses in
	// NSSKeyHostsWindow.
	Hosts int64
	// LastSeenAt is the latest lookup in the retained usage, nil for none.
	LastSeenAt *time.Time
	// MissRateAlertAt and KeySourceAlertAt are when those alerts last
	// fired, nil unless within NSSKeyAlertLookback.
	MissRateAlertAt  *time.Time
	KeySourceAlertAt *time.Time

	// Matched is, for an address search only, the matching addresses.
	Matched *NSSKeyAddressMatches
}

// NSSKeyAddressMatches is what an address search found for one key: up to
// nssKeyMatchedShown addresses, the most recently seen first, and how many
// matched in all.
type NSSKeyAddressMatches struct {
	Addresses []NSSKeyAddressMatch
	Total     int64
}

// NSSKeyAddressMatch is one address an address search matched: when the
// key was last used from it in the window, and which secret that lookup
// presented.
type NSSKeyAddressMatch struct {
	SourceIP   string
	LastSeen   time.Time
	Generation int
	Secret     model.NSSSecretGeneration
}

// nssAdminKeyRow is one selected key row with its creator. The embedded
// key is read without its secret hashes (see nssAdminKeyColumns).
type nssAdminKeyRow struct {
	model.NSSKey
	CreatorID       *string `gorm:"column:creator_id"`
	CreatorUsername *string `gorm:"column:creator_username"`
	CreatorEmail    *string `gorm:"column:creator_email"`
}

// nssAdminKeyColumns is every key column the list shows. The secret hashes
// are left out on purpose, so they never leave the database for this list.
const nssAdminKeyColumns = "nss_keys.id, nss_keys.name, nss_keys.group_name, nss_keys.current_generation," +
	" nss_keys.created_by, nss_keys.expires_at, nss_keys.previous_expires_at, nss_keys.revoked_at," +
	" nss_keys.rotated_at, nss_keys.created_at, nss_keys.updated_at," +
	" users.id AS creator_id, users.username AS creator_username, users.email AS creator_email"

// addressUsageWhere is the condition on nss_key_usage for an address
// search: the source key in the search's range, on a retained day. Served
// by idx_nss_key_usage_source_day.
func (s *NSSKeyService) addressUsageWhere(address *NSSKeyAddressSearch, now time.Time) (string, []any) {
	where, args := "nss_key_usage.source_ip_key = ?", []any{address.First}
	if address.Range {
		where, args = "nss_key_usage.source_ip_key BETWEEN ? AND ?", []any{address.First, address.Last}
	}
	if cutoff, ok := nssUsageCutoff(now, s.config.NSS.UsageRetention); ok {
		where += " AND nss_key_usage.day >= ?"
		args = append(args, cutoff)
	}
	return where, args
}

// AdminList is the admin key list: one page of every key, whoever manages
// it, narrowed by filter, by name then id. The caller is an auditor or
// above, or a fleet key admin; the route's middleware decides that. An address search reads
// nss_key_usage through its source index, and the per-key counts are
// read for the page's keys only, a fixed number of queries per page.
func (s *NSSKeyService) AdminList(ctx context.Context, filter NSSAdminKeyFilter) (*NSSAdminKeyPage, error) {
	address, err := ParseNSSKeyAddressSearch(filter.Page.Query)
	if err != nil {
		return nil, err
	}
	if filter.Page.Limit <= 0 {
		filter.Page.Limit = paging.DefaultLimit
	}
	now := s.now()
	db := s.db.WithContext(ctx)

	q := db.Table("nss_keys").Joins("LEFT JOIN users ON users.id = nss_keys.created_by")
	if address != nil {
		where, args := s.addressUsageWhere(address, now)
		q = q.Where("nss_keys.id IN (?)", db.Model(&model.NSSKeyUsage{}).Select("nss_key_usage.key_id").Where(where, args...))
	} else if where, args := nssAdminKeyTextWhere(filter.Page.Query); where != "" {
		q = q.Where(where, args...)
	}
	q = s.adminKeyFilters(q, filter, now)

	total, err := paging.Count(q.Session(&gorm.Session{}))
	if err != nil {
		return nil, fmt.Errorf("count fleet keys: %w", err)
	}
	if filter.Page.After != "" {
		where, args, err := nssViewerSeek(db, "nss_keys", filter.Page.After)
		if err != nil {
			return nil, err
		}
		q = q.Where(where, args...)
	}
	var rows []nssAdminKeyRow
	if err := q.Select(nssAdminKeyColumns).Order("nss_keys.name, nss_keys.id").
		Limit(filter.Page.Limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list fleet keys: %w", err)
	}
	rows, next := paging.Trim(rows, filter.Page.Limit, func(r nssAdminKeyRow) string { return r.ID })

	page := &NSSAdminKeyPage{
		Keys: make([]NSSAdminKeyEntry, 0, len(rows)), Total: total, NextCursor: next,
		Address: address, UsageRetention: s.config.NSS.UsageRetention,
	}
	if len(rows) == 0 {
		return page, nil
	}
	if err := s.fillAdminKeyEntries(ctx, page, rows, address, now); err != nil {
		return nil, err
	}
	return page, nil
}

// nssAdminKeyTextWhere is a text search's WHERE fragment: a substring of
// the key's name or group or the creator's username or email, ignoring
// case, or the start of the key's id. An id is "nss_" and eight lower-case
// base32 symbols (newNSSKeyID), so a whole id or a prefix of one, pasted
// from an alert or `ssoosshd-nss status` in either case, finds its key.
// The id is a plain column, never a JSON payload. "" when q is empty.
func nssAdminKeyTextWhere(q string) (string, []any) {
	text, textArgs := paging.Filter(q, "nss_keys.name", "nss_keys.group_name", "users.username", "users.email")
	if text == "" {
		return "", nil
	}
	id, idArgs := paging.PrefixFilter(q, "nss_keys.id")
	return "(" + text + " OR " + id + ")", append(textArgs, idArgs...)
}

// adminKeyFilters applies the status, expiry, unused and alert filters.
// Times are bound in UTC because SQLite compares the stored timestamps as
// text (see server/dbtime).
func (s *NSSKeyService) adminKeyFilters(q *gorm.DB, filter NSSAdminKeyFilter, now time.Time) *gorm.DB {
	utc := now.UTC()
	switch filter.Status {
	case model.NSSKeyStatusActive:
		q = q.Where("nss_keys.revoked_at IS NULL AND (nss_keys.expires_at IS NULL OR nss_keys.expires_at > ?)", utc)
	case model.NSSKeyStatusExpired:
		q = q.Where("nss_keys.revoked_at IS NULL AND nss_keys.expires_at IS NOT NULL AND nss_keys.expires_at <= ?", utc)
	case model.NSSKeyStatusRevoked:
		q = q.Where("nss_keys.revoked_at IS NOT NULL")
	}
	if filter.ExpiringWithin > 0 {
		q = q.Where("nss_keys.revoked_at IS NULL AND nss_keys.expires_at IS NOT NULL AND nss_keys.expires_at > ? AND nss_keys.expires_at <= ?",
			utc, utc.Add(filter.ExpiringWithin))
	}
	if filter.Unused {
		since := utc.Add(-NSSKeyUnusedWindow)
		q = q.Where(`NOT EXISTS (SELECT 1 FROM nss_key_usage AS recent
			WHERE recent.key_id = nss_keys.id AND recent.day >= ? AND recent.last_seen >= ?)`, nssDayStart(since), since)
	}
	if filter.Alerts {
		q = q.Where(`EXISTS (SELECT 1 FROM nss_alerts
			WHERE nss_alerts.kind IN ? AND nss_alerts.subject = nss_keys.id AND nss_alerts.last_raised_at >= ?)`,
			[]string{nssAlertMissRate, nssAlertKeySource}, utc.Add(-NSSKeyAlertLookback))
	}
	return q
}

// fillAdminKeyEntries builds the page's entries from rows: the status and
// creator from the row, the last use and alerts through activity, the host
// counts in one grouped query, and an address search's matches in two.
func (s *NSSKeyService) fillAdminKeyEntries(ctx context.Context, page *NSSAdminKeyPage, rows []nssAdminKeyRow,
	address *NSSKeyAddressSearch, now time.Time) error {
	keys := make([]model.NSSKey, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.NSSKey)
		ids = append(ids, row.ID)
	}
	activity, err := s.activity(ctx, keys, now)
	if err != nil {
		return err
	}
	hosts, err := s.hostCounts(ctx, ids, now)
	if err != nil {
		return err
	}
	var matches map[string]*NSSKeyAddressMatches
	if address != nil {
		if matches, err = s.addressMatches(ctx, rows, address, now); err != nil {
			return err
		}
	}

	for _, row := range rows {
		entry := NSSAdminKeyEntry{
			Key:          row.NSSKey,
			Status:       nssKeyStatus(&row.NSSKey, now),
			CreatorFound: row.CreatorID != nil,
			Hosts:        hosts[row.ID],
		}
		if row.CreatorUsername != nil {
			entry.CreatorUsername = *row.CreatorUsername
		}
		if row.CreatorEmail != nil {
			entry.CreatorEmail = *row.CreatorEmail
		}
		if seen := activity[row.ID]; seen != nil {
			entry.LastSeenAt = seen.lastUsed
			entry.MissRateAlertAt = seen.missRateAlert
			entry.KeySourceAlertAt = seen.keySourceAlert
		}
		if address != nil {
			entry.Matched = matches[row.ID]
		}
		page.Keys = append(page.Keys, entry)
	}
	return nil
}

// hostCounts counts, for each of ids, the distinct source addresses seen
// in NSSKeyHostsWindow: one grouped query on idx_nss_key_usage_key_day.
func (s *NSSKeyService) hostCounts(ctx context.Context, ids []string, now time.Time) (map[string]int64, error) {
	since := now.UTC().Add(-NSSKeyHostsWindow)
	var counts []struct {
		KeyID string `gorm:"column:key_id"`
		Hosts int64  `gorm:"column:hosts"`
	}
	if err := s.db.WithContext(ctx).Model(&model.NSSKeyUsage{}).
		Select("key_id, COUNT(DISTINCT source_ip) AS hosts").
		Where("key_id IN ? AND day >= ? AND last_seen >= ?", ids, nssDayStart(since), since).
		Group("key_id").Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("count each fleet key's hosts: %w", err)
	}
	out := make(map[string]int64, len(counts))
	for _, count := range counts {
		out[count.KeyID] = count.Hosts
	}
	return out, nil
}

// addressMatches reads, for the page's keys, the addresses an address
// search matched: each address's latest row in the window (so its
// generation is the last one it presented), the nssKeyMatchedShown most
// recent per key, and each key's count of matching addresses. Window
// functions rather than MAX(last_seen), because an aggregate over a
// timestamp comes back from SQLite as text that will not scan into a
// time.Time (see usageByAddress).
func (s *NSSKeyService) addressMatches(ctx context.Context, rows []nssAdminKeyRow, address *NSSKeyAddressSearch,
	now time.Time) (map[string]*NSSKeyAddressMatches, error) {
	ids := make([]string, 0, len(rows))
	generations := make(map[string]int, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		generations[row.ID] = row.CurrentGeneration
	}
	where, args := s.addressUsageWhere(address, now)
	where = "nss_key_usage.key_id IN ? AND " + where
	args = append([]any{ids}, args...)
	db := s.db.WithContext(ctx)

	var latest []struct {
		KeyID      string    `gorm:"column:key_id"`
		SourceIP   string    `gorm:"column:source_ip"`
		Generation int       `gorm:"column:generation"`
		LastSeen   time.Time `gorm:"column:last_seen"`
	}
	// The inner select keeps each address's newest row, the outer the
	// newest addresses per key.
	sql := strings.Join([]string{
		"SELECT key_id, source_ip, generation, last_seen FROM (",
		"  SELECT key_id, source_ip, generation, last_seen,",
		"    ROW_NUMBER() OVER (PARTITION BY key_id ORDER BY last_seen DESC, source_ip) AS key_rank",
		"  FROM (",
		"    SELECT nss_key_usage.key_id, nss_key_usage.source_ip, nss_key_usage.generation, nss_key_usage.last_seen,",
		"      ROW_NUMBER() OVER (PARTITION BY nss_key_usage.key_id, nss_key_usage.source_ip",
		"        ORDER BY nss_key_usage.last_seen DESC, nss_key_usage.generation DESC) AS address_rank",
		"    FROM nss_key_usage WHERE " + where,
		"  ) AS per_address WHERE address_rank = 1",
		") AS per_key WHERE key_rank <= ? ORDER BY key_id, key_rank",
	}, "\n")
	if err := db.Raw(sql, append(args, nssKeyMatchedShown)...).Scan(&latest).Error; err != nil {
		return nil, fmt.Errorf("read the addresses each fleet key was used from: %w", err)
	}

	var totals []struct {
		KeyID     string `gorm:"column:key_id"`
		Addresses int64  `gorm:"column:addresses"`
	}
	if err := db.Model(&model.NSSKeyUsage{}).Select("key_id, COUNT(DISTINCT source_ip) AS addresses").
		Where(where, args...).Group("key_id").Scan(&totals).Error; err != nil {
		return nil, fmt.Errorf("count the addresses each fleet key was used from: %w", err)
	}

	// Every key of the page gets an entry, so one a prune emptied between
	// the reads shows no addresses rather than none at all.
	out := make(map[string]*NSSKeyAddressMatches, len(rows))
	for _, id := range ids {
		out[id] = &NSSKeyAddressMatches{Addresses: []NSSKeyAddressMatch{}}
	}
	for _, total := range totals {
		out[total.KeyID].Total = total.Addresses
	}
	for _, use := range latest {
		matched := out[use.KeyID]
		matched.Addresses = append(matched.Addresses, NSSKeyAddressMatch{
			SourceIP: use.SourceIP, LastSeen: use.LastSeen, Generation: use.Generation,
			Secret: secretGeneration(use.Generation, generations[use.KeyID]),
		})
	}
	return out, nil
}

// secretGeneration names presented relative to a key at current.
func secretGeneration(presented, current int) model.NSSSecretGeneration {
	switch {
	case presented >= current:
		return model.NSSSecretGenerationCurrent
	case presented == current-1:
		return model.NSSSecretGenerationPrevious
	default:
		return model.NSSSecretGenerationOlder
	}
}
