package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// NSSKeyUsagePage is one page of a key's usage rows, newest day first.
type NSSKeyUsagePage struct {
	Rows []model.NSSKeyUsage
	// Total counts every retained row for the key, across all pages.
	Total int64
	// NextCursor is the after for the following page, nil on the last.
	NextCursor *string
}

// Usage returns one page of a key's usage rows, ordered by day newest
// first, then source address, then generation newest first. The cursor
// names a row by those three, since the table has no single id.
func (s *NSSKeyService) Usage(ctx context.Context, identity *Identity, id string, params paging.Params) (*NSSKeyUsagePage, error) {
	if _, err := s.Get(ctx, identity, id); err != nil {
		return nil, err
	}

	q := s.db.WithContext(ctx).Model(&model.NSSKeyUsage{}).Where("key_id = ?", id)
	total, err := paging.Count(q.Session(&gorm.Session{}))
	if err != nil {
		return nil, fmt.Errorf("count fleet key usage: %w", err)
	}
	if params.After != "" {
		day, ip, generation, ok := parseNSSUsageCursor(params.After)
		if !ok {
			return nil, paging.UnknownCursor()
		}
		q = q.Where("day <= ? AND (day < ? OR (day = ? AND (source_ip > ? OR (source_ip = ? AND generation < ?))))",
			day, day, day, ip, ip, generation)
	}

	var rows []model.NSSKeyUsage
	if err := q.Order("day DESC, source_ip ASC, generation DESC").Limit(params.Limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list fleet key usage: %w", err)
	}
	rows, next := paging.Trim(rows, params.Limit, nssUsageCursor)
	return &NSSKeyUsagePage{Rows: rows, Total: total, NextCursor: next}, nil
}

// nssUsageCursor names a usage row for the next page: its day as Unix
// seconds, its generation, and its source address last, because an IPv6
// address holds the separator the other two never do.
func nssUsageCursor(row model.NSSKeyUsage) string {
	return strconv.FormatInt(row.Day.Unix(), 10) + "." + strconv.Itoa(row.Generation) + "." + row.SourceIP
}

// parseNSSUsageCursor reads a cursor nssUsageCursor wrote. ok is false for
// anything else.
func parseNSSUsageCursor(cursor string) (time.Time, string, int, bool) {
	parts := strings.SplitN(cursor, ".", 3)
	if len(parts) != 3 || parts[2] == "" {
		return time.Time{}, "", 0, false
	}
	unix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", 0, false
	}
	generation, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, "", 0, false
	}
	return time.Unix(unix, 0).UTC(), parts[2], generation, true
}

// NSSRotationReportRow is one source address in the rotation report.
// FirstSeen, LastSeen and Lookups cover every retained usage row, before
// the rotation as well as after, which is what lets a gone host be named.
type NSSRotationReportRow struct {
	SourceIP       string
	FirstSeen      time.Time
	LastSeen       time.Time
	Lookups        int64
	LastGeneration int
	Bucket         model.NSSRotationBucket
}

// NSSRotationReport is where every address that used a key stands since it
// was last rotated: the rows, not_switched first, then by last seen
// newest first, and the count in each bucket.
type NSSRotationReport struct {
	KeyID             string
	CurrentGeneration int
	RotatedAt         *time.Time
	PreviousExpiresAt *time.Time
	// GraceDaysLeft is whole days, rounded up, until the previous secret
	// stops working; zero when there is none or it has ended.
	GraceDaysLeft int
	Switched      int
	NotSwitched   int
	Gone          int
	Rows          []NSSRotationReportRow
}

// RotationReport reports, for a key the identity may view, which source
// addresses have switched to the current secret.
func (s *NSSKeyService) RotationReport(ctx context.Context, identity *Identity, id string) (*NSSRotationReport, error) {
	view, err := s.Get(ctx, identity, id)
	if err != nil {
		return nil, err
	}
	return s.rotationReport(ctx, &view.Key)
}

// rotationReport builds the report for key. The rotation cleanup digest
// reads it too, so the mail and the page never disagree.
func (s *NSSKeyService) rotationReport(ctx context.Context, key *model.NSSKey) (*NSSRotationReport, error) {
	rows, err := s.usageByAddress(ctx, key.ID)
	if err != nil {
		return nil, err
	}

	report := &NSSRotationReport{
		KeyID:             key.ID,
		CurrentGeneration: key.CurrentGeneration,
		RotatedAt:         key.RotatedAt,
		PreviousExpiresAt: key.PreviousExpiresAt,
		Rows:              make([]NSSRotationReportRow, 0, len(rows)),
	}
	if key.PreviousExpiresAt != nil {
		if left := key.PreviousExpiresAt.Sub(s.now()); left > 0 {
			report.GraceDaysLeft = int(math.Ceil(left.Hours() / 24))
		}
	}
	for _, row := range rows {
		row.Bucket = rotationBucket(row, key)
		switch row.Bucket {
		case model.NSSRotationBucketSwitched:
			report.Switched++
		case model.NSSRotationBucketNotSwitched:
			report.NotSwitched++
		default:
			report.Gone++
		}
		report.Rows = append(report.Rows, *row)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		a, b := report.Rows[i], report.Rows[j]
		if rank(a.Bucket) != rank(b.Bucket) {
			return rank(a.Bucket) < rank(b.Bucket)
		}
		if !a.LastSeen.Equal(b.LastSeen) {
			return a.LastSeen.After(b.LastSeen)
		}
		return a.SourceIP < b.SourceIP
	})
	return report, nil
}

// usageByAddress summarises one key's retained usage per source address,
// without the bucket.
//
// Three reads rather than one aggregate, because an aggregate over a
// timestamp comes back from SQLite as text that will not scan into a
// time.Time (see EnrollmentService.latestRetrievals): each address's latest
// row (its last generation and last seen), its earliest row (first seen),
// and its lookup total.
func (s *NSSKeyService) usageByAddress(ctx context.Context, keyID string) (map[string]*NSSRotationReportRow, error) {
	db := s.db.WithContext(ctx)
	var latest, earliest []model.NSSKeyUsage
	if err := db.Where("key_id = ?", keyID).Where(`last_seen = (
			SELECT MAX(inner_usage.last_seen) FROM nss_key_usage AS inner_usage
			WHERE inner_usage.key_id = nss_key_usage.key_id AND inner_usage.source_ip = nss_key_usage.source_ip
		)`).Find(&latest).Error; err != nil {
		return nil, fmt.Errorf("read each address's latest fleet key use: %w", err)
	}
	if err := db.Where("key_id = ?", keyID).Where(`first_seen = (
			SELECT MIN(inner_usage.first_seen) FROM nss_key_usage AS inner_usage
			WHERE inner_usage.key_id = nss_key_usage.key_id AND inner_usage.source_ip = nss_key_usage.source_ip
		)`).Find(&earliest).Error; err != nil {
		return nil, fmt.Errorf("read each address's first fleet key use: %w", err)
	}
	var totals []struct {
		SourceIP string `gorm:"column:source_ip"`
		Lookups  int64  `gorm:"column:lookups"`
	}
	if err := db.Model(&model.NSSKeyUsage{}).Select("source_ip, SUM(lookups) AS lookups").
		Where("key_id = ?", keyID).Group("source_ip").Scan(&totals).Error; err != nil {
		return nil, fmt.Errorf("total each address's fleet key use: %w", err)
	}

	rows := map[string]*NSSRotationReportRow{}
	for _, use := range latest {
		row, ok := rows[use.SourceIP]
		// Two generations can share the latest instant; the newer wins.
		if !ok || use.Generation > row.LastGeneration {
			rows[use.SourceIP] = &NSSRotationReportRow{SourceIP: use.SourceIP, LastSeen: use.LastSeen, LastGeneration: use.Generation}
		}
	}
	for _, use := range earliest {
		if row, ok := rows[use.SourceIP]; ok {
			row.FirstSeen = use.FirstSeen
		}
	}
	for _, total := range totals {
		if row, ok := rows[total.SourceIP]; ok {
			row.Lookups = total.Lookups
		}
	}

	return rows, nil
}

// rotationBucket places one address: gone when it has not been seen since
// the rotation, else switched or not by the generation it last presented.
func rotationBucket(row *NSSRotationReportRow, key *model.NSSKey) model.NSSRotationBucket {
	if key.RotatedAt != nil && row.LastSeen.Before(*key.RotatedAt) {
		return model.NSSRotationBucketGone
	}
	if row.LastGeneration >= key.CurrentGeneration {
		return model.NSSRotationBucketSwitched
	}
	return model.NSSRotationBucketNotSwitched
}

// rank orders the buckets for the report: the addresses that need action
// first.
func rank(bucket model.NSSRotationBucket) int {
	switch bucket {
	case model.NSSRotationBucketNotSwitched:
		return 0
	case model.NSSRotationBucketSwitched:
		return 1
	default:
		return 2
	}
}
