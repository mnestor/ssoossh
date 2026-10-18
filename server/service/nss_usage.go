package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Timing of the usage aggregate and its alerts.
const (
	// NSSUsageFlushInterval is how often the in-memory counts are written
	// to nss_key_usage. A crash loses at most one interval of counts,
	// which is acceptable for a reporting table.
	NSSUsageFlushInterval = time.Minute

	// nssMissWindow is the window the per-key miss rate is measured over.
	nssMissWindow = time.Minute

	// nssAlertDebounce is how long one instance waits before handing the
	// same alert to the database claim again. The claim is what holds an
	// alert to its window across instances; this only keeps a flood of
	// identical events from costing a write each.
	nssAlertDebounce = time.Minute

	// nssEventQueue bounds the alerts waiting for the background loop. An
	// alert that does not fit is dropped with a debug line: the next
	// matching lookup raises it again, and a lookup never waits on one.
	nssEventQueue = 256

	// nssUsageBatch bounds the rows in one upsert statement.
	nssUsageBatch = 500

	// maxNSSPresentedKeyID caps the presented key id an audit row and an
	// alert claim carry. The value is the caller's, so it is bounded
	// before it is stored.
	maxNSSPresentedKeyID = 64
)

// The alert windows. Revoked, expired and forbidden attempts are hourly
// because they describe something happening now; the usage alerts are
// daily because they describe a day.
const (
	nssAttemptAlertWindow = time.Hour
	nssUsageAlertWindow   = 24 * time.Hour
)

// The kinds the alerts are claimed under in nss_alerts.
const (
	nssAlertLookupForbidden = "lookup_forbidden"
	nssAlertRevokedAttempt  = "key_revoked_attempt"
	nssAlertExpiredAttempt  = "key_expired_attempt"
	nssAlertKeySource       = "key_source"
	nssAlertMissRate        = "miss_rate"
	nssAlertPrivilegedName  = "privileged_name"
)

// NSSLookupObserver is what the fleet key middleware reports every
// finished lookup to. NSSUsageService implements it.
type NSSLookupObserver interface {
	Observe(ctx context.Context, o NSSLookupOutcome)
}

// NSSLookupOutcome is everything one finished lookup reports: what was
// asked and how it ended, for the metrics; the key and address, for the
// usage aggregate and the alerts.
type NSSLookupOutcome struct {
	// Kind is NSSKindUser or NSSKindGroup, By NSSByName or NSSByID, and
	// Result one of the NSSResult* values.
	Kind   string
	By     string
	Result string

	// Verdict is the fleet key check, zero when the request never reached
	// it (a refused source address).
	Verdict NSSKeyVerdict
	// KeyID is the key id the request presented, verified or not.
	KeyID string
	// SourceIP is the address the server observed (ClientIP).
	SourceIP string
	// Name is the name looked up, empty for a lookup by id.
	Name string

	// Duration is the time from the request reaching the NSS chain to its
	// answer.
	Duration time.Duration
}

// nssUsageKey is one row of nss_key_usage.
type nssUsageKey struct {
	keyID      string
	sourceIP   string
	generation int
	day        time.Time
}

// nssUsageCount is one row's counts since the last flush.
type nssUsageCount struct {
	lookups   int64
	misses    int64
	firstSeen time.Time
	lastSeen  time.Time
}

// nssMissCount is one key's misses in the current miss window.
type nssMissCount struct {
	start  time.Time
	misses int
	raised bool
}

// nssAlert is one alert on its way to the background loop: claimed in
// nss_alerts under kind and subject for window, and recorded as event when
// the claim is won. attempt, when set, is a dead key presentation whose
// mail has its own claim.
type nssAlert struct {
	kind    string
	subject string
	window  time.Duration
	event   AuditEvent
	attempt *nssDeadKeyAttempt
}

// nssDeadKeyAttempt is one presentation of a revoked or expired key.
type nssDeadKeyAttempt struct {
	key      model.NSSKey
	dead     string
	sourceIP string
	at       time.Time
}

// NSSUsageService turns finished lookups into the things that outlive
// them: the lookup metrics, the nss_key_usage aggregate behind the usage
// and rotation reports, and the alerts. Nothing it does is on a lookup's
// critical path beyond a map update: counts are flushed every
// NSSUsageFlushInterval and on shutdown, and alerts are raised by the
// background loop, so an alert never delays or refuses a lookup.
//
// The counts, the miss windows and the debounce are per instance; the
// flush is an additive upsert and every alert is claimed in the database,
// so several instances together still write correct totals and raise each
// alert once per window (README R28).
type NSSUsageService struct {
	config *config.Config
	db     *gorm.DB
	keys   *NSSKeyService

	// auditor records the alerts. Nil until SetAuditor runs.
	auditor *AuditService

	metrics nssLookupMetrics
	events  chan nssAlert

	// flushInterval is NSSUsageFlushInterval outside tests.
	flushInterval time.Duration

	// now is the clock, time.Now outside tests.
	now func() time.Time

	mu      sync.Mutex
	counts  map[nssUsageKey]*nssUsageCount
	misses  map[string]*nssMissCount
	queued  map[string]time.Time
	dropped int
}

// NewNSSUsageService builds the aggregate. keys sends the dead key
// attempt mail; meter carries the lookup metrics.
func NewNSSUsageService(c *config.Config, db *gorm.DB, keys *NSSKeyService, meter metric.Meter) (*NSSUsageService, error) {
	metrics, err := newNSSLookupMetrics(meter)
	if err != nil {
		// not covered: see newNSSLookupMetrics.
		return nil, err
	}
	return &NSSUsageService{
		config:  c,
		db:      db,
		keys:    keys,
		metrics: metrics,
		events:  make(chan nssAlert, nssEventQueue),
		now:     time.Now,

		flushInterval: NSSUsageFlushInterval,
		counts:        map[nssUsageKey]*nssUsageCount{},
		misses:        map[string]*nssMissCount{},
		queued:        map[string]time.Time{},
	}, nil
}

// SetAuditor wires the audit recorder. Called once at startup.
func (s *NSSUsageService) SetAuditor(a *AuditService) { s.auditor = a }

// Observe records one finished lookup. Cheap and never blocking: a metric,
// a map update, and at most a queued alert.
func (s *NSSUsageService) Observe(ctx context.Context, o NSSLookupOutcome) {
	s.metrics.record(ctx, o.Kind, o.By, o.Result, o.Duration)
	now := s.now()

	switch {
	case o.Result == NSSResultForbidden:
		s.queueForbidden(o, now)
	case o.Verdict.Dead != "" && o.Verdict.Key != nil:
		s.queueDeadKey(ctx, o, now)
	}
	if !o.Verdict.OK {
		return
	}

	// Only answered lookups count as usage: a rate-limited one was never
	// served, and the reports are about what hosts were given.
	switch o.Result {
	case NSSResultHit, NSSResultMiss, NSSResultBadRequest:
	default:
		return
	}
	miss := o.Result == NSSResultMiss
	s.count(o, miss, now)
	if miss {
		s.countMiss(o.Verdict.KeyID, now)
	}
	if o.Name != "" && slices.Contains(s.config.NSS.PrivilegedNameAlert, o.Name) {
		s.queue(nssAlert{
			kind:    nssAlertPrivilegedName,
			subject: o.Verdict.KeyID + "|" + o.Name,
			window:  nssUsageAlertWindow,
			event: AuditEvent{
				Action: AuditNSSPrivilegedNameLookup,
				System: true,
				Detail: map[string]any{"key_id": o.Verdict.KeyID, "name": o.Name, "kind": o.Kind, "lookup_source_ip": o.SourceIP},
			},
		}, now)
	}
}

// count adds one lookup to its row of the aggregate.
func (s *NSSUsageService) count(o NSSLookupOutcome, miss bool, now time.Time) {
	now = now.UTC()
	key := nssUsageKey{
		keyID:      o.Verdict.KeyID,
		sourceIP:   o.SourceIP,
		generation: o.Verdict.Generation,
		day:        time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.counts[key]
	if !ok {
		row = &nssUsageCount{firstSeen: now}
		s.counts[key] = row
	}
	row.lookups++
	if miss {
		row.misses++
	}
	row.lastSeen = now
}

// countMiss adds one miss to the key's window and queues the miss rate
// alert the first time the window passes nss.miss_alert_rate.
func (s *NSSUsageService) countMiss(keyID string, now time.Time) {
	rate := s.config.NSS.MissAlertRate
	if rate <= 0 {
		return
	}
	s.mu.Lock()
	window, ok := s.misses[keyID]
	if !ok || now.Sub(window.start) >= nssMissWindow {
		window = &nssMissCount{start: now}
		s.misses[keyID] = window
	}
	window.misses++
	crossed := !window.raised && float64(window.misses) > rate*nssMissWindow.Seconds()
	if crossed {
		window.raised = true
	}
	misses := window.misses
	s.mu.Unlock()

	if crossed {
		s.queue(nssAlert{
			kind:    nssAlertMissRate,
			subject: keyID,
			window:  nssUsageAlertWindow,
			event: AuditEvent{
				Action: AuditNSSMissRateAlert,
				System: true,
				Detail: map[string]any{
					"key_id":          keyID,
					"misses":          misses,
					"window_seconds":  nssMissWindow.Seconds(),
					"miss_alert_rate": rate,
				},
			},
		}, now)
	}
}

// queueForbidden queues the audit of a lookup refused for its address.
func (s *NSSUsageService) queueForbidden(o NSSLookupOutcome, now time.Time) {
	keyID := boundedKeyID(o.KeyID)
	s.queue(nssAlert{
		kind:    nssAlertLookupForbidden,
		subject: o.SourceIP + "|" + keyID,
		window:  nssAttemptAlertWindow,
		event: AuditEvent{
			Action: AuditNSSLookupForbidden,
			System: true,
			Detail: map[string]any{"source_ip": o.SourceIP, "key_id": keyID},
		},
	}, now)
}

// queueDeadKey logs a presentation of a revoked or expired key, every one
// at debug, and queues its hourly audit and its mail.
func (s *NSSUsageService) queueDeadKey(ctx context.Context, o NSSLookupOutcome, now time.Time) {
	key := *o.Verdict.Key
	slog.DebugContext(ctx, "an NSS lookup presented a fleet key that can no longer be used",
		"key_id", key.ID, "state", o.Verdict.Dead, "source_ip", o.SourceIP)

	kind, action := nssAlertRevokedAttempt, AuditNSSKeyRevokedAttempt
	if o.Verdict.Dead == NSSDeadKeyExpired {
		kind, action = nssAlertExpiredAttempt, AuditNSSKeyExpiredAttempt
	}
	s.queue(nssAlert{
		kind:    kind,
		subject: key.ID,
		window:  nssAttemptAlertWindow,
		event: AuditEvent{
			Action: action,
			System: true,
			Detail: map[string]any{"key_id": key.ID, "name": key.Name, "source_ip": o.SourceIP},
		},
		attempt: &nssDeadKeyAttempt{key: key, dead: o.Verdict.Dead, sourceIP: o.SourceIP, at: now},
	}, now)
}

// boundedKeyID caps a presented key id before it is stored.
func boundedKeyID(id string) string {
	if len(id) > maxNSSPresentedKeyID {
		return id[:maxNSSPresentedKeyID]
	}
	return id
}

// queue hands an alert to the background loop unless this instance handed
// the same one over inside nssAlertDebounce. Never blocks: a full queue
// drops the alert.
func (s *NSSUsageService) queue(alert nssAlert, now time.Time) {
	debounceKey := alert.kind + "|" + alert.subject
	s.mu.Lock()
	if last, ok := s.queued[debounceKey]; ok && now.Sub(last) < nssAlertDebounce {
		s.mu.Unlock()
		return
	}
	s.queued[debounceKey] = now
	s.mu.Unlock()

	alert.event.OccurredAt = now
	select {
	case s.events <- alert:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
		slog.Debug("dropped an NSS alert: the alert queue is full", "kind", alert.kind)
	}
}

// Run is the background loop: it raises queued alerts as they arrive and
// flushes the aggregate every NSSUsageFlushInterval, until ctx ends.
// Shutdown does the last flush, with a context of its own, because ctx is
// already cancelled when this returns.
func (s *NSSUsageService) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case alert := <-s.events:
			s.raise(ctx, alert)
		case <-ticker.C:
			if err := s.Flush(ctx); err != nil {
				slog.ErrorContext(ctx, "failed to flush the NSS key usage counts; they are kept for the next flush", "error", err)
			}
		}
	}
}

// Shutdown raises the alerts still queued and flushes the aggregate, so a
// graceful stop loses no counts. Registered as a shutdown hook.
func (s *NSSUsageService) Shutdown(ctx context.Context) error {
	for {
		select {
		case alert := <-s.events:
			s.raise(ctx, alert)
		default:
			return s.Flush(ctx)
		}
	}
}

// raise claims one alert and records it when the claim is won, then sends
// a dead key attempt's mail, which is claimed separately.
func (s *NSSUsageService) raise(ctx context.Context, alert nssAlert) {
	won, err := claimNSSAlert(ctx, s.db, alert.kind, alert.subject, alert.event.OccurredAt, alert.window)
	if err != nil {
		slog.ErrorContext(ctx, "failed to claim an NSS alert", "kind", alert.kind, "error", err)
	} else if won && s.auditor != nil {
		s.auditor.Record(ctx, alert.event)
	}
	if alert.attempt != nil && s.keys != nil {
		s.keys.notifyDeadKeyAttempt(ctx, alert.attempt)
	}
}

// Flush writes the counts gathered since the last flush to nss_key_usage
// and checks the keys it wrote for the key source alert. The upsert is
// additive (lookups and misses summed, first_seen the earlier, last_seen
// the later), so every instance flushing its own counts adds up to the
// right totals. On failure the counts are put back for the next flush.
func (s *NSSUsageService) Flush(ctx context.Context) error {
	s.mu.Lock()
	pending := s.counts
	s.counts = map[nssUsageKey]*nssUsageCount{}
	s.pruneLocked(s.now())
	s.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}

	rows := make([]model.NSSKeyUsage, 0, len(pending))
	keyIDs := map[string]bool{}
	for key, count := range pending {
		rows = append(rows, model.NSSKeyUsage{
			KeyID: key.keyID, SourceIP: key.sourceIP, Generation: key.generation, Day: key.day,
			Lookups: count.lookups, Misses: count.misses, FirstSeen: count.firstSeen, LastSeen: count.lastSeen,
		})
		keyIDs[key.keyID] = true
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key_id"}, {Name: "source_ip"}, {Name: "generation"}, {Name: "day"}},
			DoUpdates: clause.Set{
				{Column: clause.Column{Name: "lookups"}, Value: gorm.Expr("nss_key_usage.lookups + excluded.lookups")},
				{Column: clause.Column{Name: "misses"}, Value: gorm.Expr("nss_key_usage.misses + excluded.misses")},
				{Column: clause.Column{Name: "first_seen"}, Value: gorm.Expr("CASE WHEN excluded.first_seen < nss_key_usage.first_seen THEN excluded.first_seen ELSE nss_key_usage.first_seen END")},
				{Column: clause.Column{Name: "last_seen"}, Value: gorm.Expr("CASE WHEN excluded.last_seen > nss_key_usage.last_seen THEN excluded.last_seen ELSE nss_key_usage.last_seen END")},
			},
		}).CreateInBatches(rows, nssUsageBatch).Error
	})
	if err != nil {
		s.restore(pending)
		return fmt.Errorf("flush nss key usage: %w", err)
	}

	ids := make([]string, 0, len(keyIDs))
	for id := range keyIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	s.checkKeySources(ctx, ids)
	return nil
}

// restore merges counts that failed to flush back into the live map.
func (s *NSSUsageService) restore(pending map[nssUsageKey]*nssUsageCount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, count := range pending {
		row, ok := s.counts[key]
		if !ok {
			s.counts[key] = count
			continue
		}
		row.lookups += count.lookups
		row.misses += count.misses
		if count.firstSeen.Before(row.firstSeen) {
			row.firstSeen = count.firstSeen
		}
		if count.lastSeen.After(row.lastSeen) {
			row.lastSeen = count.lastSeen
		}
	}
}

// pruneLocked forgets miss windows and debounce entries too old to matter.
// The caller holds s.mu.
func (s *NSSUsageService) pruneLocked(now time.Time) {
	for keyID, window := range s.misses {
		if now.Sub(window.start) >= 2*nssMissWindow {
			delete(s.misses, keyID)
		}
	}
	for key, at := range s.queued {
		if now.Sub(at) >= nssAlertDebounce {
			delete(s.queued, key)
		}
	}
}

// checkKeySources raises the key source alert for every key in keyIDs seen
// today from more distinct addresses than nss.key_source_alert. Counted in
// the database, so the addresses every instance saw are counted together.
func (s *NSSUsageService) checkKeySources(ctx context.Context, keyIDs []string) {
	limit := s.config.NSS.KeySourceAlert
	if limit <= 0 || len(keyIDs) == 0 {
		return
	}
	now := s.now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var rows []struct {
		KeyID   string `gorm:"column:key_id"`
		Sources int64  `gorm:"column:sources"`
	}
	err := s.db.WithContext(ctx).Model(&model.NSSKeyUsage{}).
		Select("key_id, COUNT(DISTINCT source_ip) AS sources").
		Where("day = ? AND key_id IN ?", day, keyIDs).
		Group("key_id").
		Having("COUNT(DISTINCT source_ip) > ?", limit).
		Scan(&rows).Error
	if err != nil {
		slog.ErrorContext(ctx, "failed to count the addresses each NSS fleet key was used from", "error", err)
		return
	}
	for _, row := range rows {
		s.raise(ctx, nssAlert{
			kind:    nssAlertKeySource,
			subject: row.KeyID,
			window:  nssUsageAlertWindow,
			event: AuditEvent{
				Action:     AuditNSSKeySourceAlert,
				System:     true,
				OccurredAt: now,
				Detail:     map[string]any{"key_id": row.KeyID, "sources": row.Sources, "key_source_alert": limit, "day": day.Format(time.DateOnly)},
			},
		})
	}
}

// PruneUsage deletes usage rows older than nss.usage_retention, whole days
// at a time. Zero retention keeps every row. Registered as a scheduled
// job; every instance runs it, and a delete is idempotent.
func (s *NSSUsageService) PruneUsage(ctx context.Context) error {
	retention := s.config.NSS.UsageRetention
	if retention <= 0 {
		return nil
	}
	cutoff := s.now().Add(-retention).UTC()
	cutoff = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC)
	result := s.db.WithContext(ctx).Where("day < ?", cutoff).Delete(&model.NSSKeyUsage{})
	if result.Error != nil {
		return fmt.Errorf("prune nss key usage: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		slog.InfoContext(ctx, "pruned NSS fleet key usage", "rows", result.RowsAffected, "before", cutoff.Format(time.DateOnly))
	}
	return nil
}
