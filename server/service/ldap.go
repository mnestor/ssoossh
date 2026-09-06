package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
)

// LDAPService enriches an OIDC identity with directory data and keeps that
// data fresh in the background.
//
// LDAP is enrichment, never a requirement: if the directory is available a
// user gets more, and if it is not, every basic operation still works on
// OIDC claims alone. Login therefore fails open — an error here logs to the
// LDAP destination and the caller proceeds with the OIDC-only identity.
type LDAPService struct {
	config *config.Config
	db     *gorm.DB
	dial   ldapDialer
	log    *slog.Logger

	// userFilter and the per-field searches are parsed once at
	// construction, so a bad template fails startup rather than every
	// login.
	userFilter *filterTemplate
	fields     []parsedLDAPField

	// primaryAttrs is every attribute the primary lookup must request:
	// the direct field attributes plus any name a search filter reads
	// through .Attr.
	primaryAttrs []string

	// groupAllowlist is the set of group names worth persisting. Anything
	// outside it is discarded at capture time: the server records the roles
	// it acts on, it does not mirror the directory's group graph.
	groupAllowlist []string

	auditor *AuditService

	// running is the single-flight guard on Sync. One pass at a time per
	// instance, so an operator pressing the button repeatedly cannot stack
	// passes over the same users.
	running atomic.Bool
}

// parsedLDAPField is one destination with its sources ready to execute.
type parsedLDAPField struct {
	name      string
	attribute string
	searches  []parsedLDAPSearch
}

// parsedLDAPSearch is one secondary search with its filter parsed.
type parsedLDAPSearch struct {
	name   string
	baseDN string
	filter *filterTemplate
	value  string
}

// NewLDAPService parses the directory configuration, failing startup on a
// bad filter template rather than on every login. Returns nil when LDAP is
// disabled, which every caller treats as "no enrichment".
func NewLDAPService(c *config.Config, db *gorm.DB) (*LDAPService, error) {
	if !c.LDAP.Enabled {
		return nil, nil //nolint:nilnil // a disabled directory is not an error; callers check for nil.
	}

	s := &LDAPService{
		config:         c,
		db:             db,
		dial:           dialLDAP,
		log:            logging.Tagged(logging.TagLDAP),
		groupAllowlist: groupAllowlist(c),
	}

	var err error
	if s.userFilter, err = parseFilterTemplate("user_filter", c.LDAP.UserFilter); err != nil {
		return nil, err
	}

	if s.fields, s.primaryAttrs, err = parseLDAPFields(c); err != nil {
		return nil, err
	}

	return s, nil
}

// parseLDAPFields parses every configured field and its secondary searches
// into the runtime shape, and collects the sorted union of attribute names
// the primary read must request (the field attributes, the re-anchoring ID,
// and anything a search filter references off the primary entry).
func parseLDAPFields(c *config.Config) (fields []parsedLDAPField, primaryAttrs []string, err error) {
	attrs := map[string]bool{}
	// The re-anchoring ID is requested on every primary read, so it is
	// captured from the very first login rather than only once a sync has
	// run. Unconfigured adds nothing to the request.
	if c.LDAP.IDAttribute != "" {
		attrs[c.LDAP.IDAttribute] = true
	}
	for _, name := range sortedFieldNames(c.LDAP.Fields) {
		field := c.LDAP.Fields[name]
		parsed := parsedLDAPField{name: name, attribute: field.Attribute}
		if field.Attribute != "" {
			attrs[field.Attribute] = true
		}
		for i, search := range field.Searches {
			label := search.Name
			if label == "" {
				label = fmt.Sprintf("%s.searches[%d]", name, i)
			}
			tmpl, perr := parseFilterTemplate(label, search.Filter)
			if perr != nil {
				return nil, nil, perr
			}
			// Attributes a search reads off the primary entry are
			// collected here and requested automatically, so an operator
			// never has to duplicate one as an extra field.
			for _, a := range tmpl.referencedAttrs {
				attrs[a] = true
			}
			baseDN := search.BaseDN
			if baseDN == "" {
				baseDN = c.LDAP.BaseDN
			}
			parsed.searches = append(parsed.searches, parsedLDAPSearch{
				name: label, baseDN: baseDN, filter: tmpl, value: search.Value,
			})
		}
		fields = append(fields, parsed)
	}
	for a := range attrs {
		primaryAttrs = append(primaryAttrs, a)
	}
	slices.Sort(primaryAttrs)
	return fields, primaryAttrs, nil
}

// SetAuditor attaches the audit recorder, so an auto-disable is recorded
// like any other containment action.
func (s *LDAPService) SetAuditor(a *AuditService) {
	if s != nil {
		s.auditor = a
	}
}

// sortedFieldNames gives map iteration a stable order, so the requested
// attribute list and any error message read the same across runs.
func sortedFieldNames(fields map[string]config.LDAPField) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// groupAllowlist is the union of every group name the configuration
// references, plus the explicit extras. Membership outside this set is
// discarded rather than stored: the table records the roles the server acts
// on, and a config change that adds a name self-heals at the next sync or
// login rather than needing a backfill.
func groupAllowlist(c *config.Config) []string {
	var names []string
	add := func(n string) {
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}

	add(c.Admin.RequireGroup)
	add(c.Admin.SOCGroup)
	add(c.Admin.AuditorGroup)
	for _, n := range c.LDAP.Sync.ExtraGroups {
		add(n)
	}

	// Group names referenced by certificate policy, from both the type
	// gates and every tier condition.
	for _, opts := range []struct {
		require *config.PolicyCondition
		policy  config.LifetimePolicy
	}{
		{c.CertOptions.User.Require, c.CertOptions.User.LifetimePolicy},
		{c.CertOptions.Service.Require, c.CertOptions.Service.LifetimePolicy},
		{c.CertOptions.PAM.Require, c.CertOptions.PAM.LifetimePolicy},
	} {
		for _, n := range conditionGroups(opts.require) {
			add(n)
		}
		for i := range opts.policy.Tiers {
			for _, n := range conditionGroups(&opts.policy.Tiers[i].When) {
				add(n)
			}
		}
	}

	slices.Sort(names)
	return names
}

// conditionGroups collects every group name a condition references,
// including nested ones.
func conditionGroups(cond *config.PolicyCondition) []string {
	if cond == nil || cond.IsZero() {
		return nil
	}
	var out []string
	if cond.Group != "" {
		out = append(out, cond.Group)
	}
	for i := range cond.AllOf {
		out = append(out, conditionGroups(&cond.AllOf[i])...)
	}
	for i := range cond.AnyOf {
		out = append(out, conditionGroups(&cond.AnyOf[i])...)
	}
	return out
}

// ldapEntry is what one directory lookup yields.
type ldapEntry struct {
	DN string
	// DirectoryID is the value of config.LDAPConfig.IDAttribute on this
	// entry, in the form directoryIDValue produced. Empty when the
	// attribute is unconfigured or absent, which leaves resolution on the
	// DN-then-filter path.
	DirectoryID string
	// Values maps each configured field name to the values resolved for
	// it, from the entry's own attribute and every search under it.
	Values map[string][]string
	// Attrs are the raw primary-entry attributes the search filters read.
	Attrs map[string]string
}

// Enrich looks up identity in the directory and merges what it finds,
// mutating identity in place. It never returns an error to the caller:
// login fails open, so a directory problem is logged and the OIDC-only
// identity proceeds unchanged.
//
// userID is the users-row id, already upserted by the caller.
func (s *LDAPService) Enrich(ctx context.Context, identity *Identity, userID string) {
	if s == nil {
		return
	}

	// The stored anchors, so a login resolves the same way a sync pass
	// does. Without them a renamed person's user_filter no longer matches
	// and every one of their logins falls back to the cache, which is the
	// case ldap.id_attribute exists to fix. A read failure is not worth
	// failing over: nil anchors just start the walk at the filter, exactly
	// as a first login does.
	anchors, err := s.storedRow(ctx, userID)
	if err != nil {
		s.log.WarnContext(ctx, "failed to read the stored directory anchors; resolving by filter",
			"subject", identity.Subject, "error", err)
	}

	entry, err := s.lookup(ctx, identity, anchors)
	if err != nil {
		s.log.WarnContext(ctx, "directory lookup failed; continuing with the OIDC identity",
			"subject", identity.Subject, "username", identity.Username, "error", err)
		// Last-known-good: a directory outage should degrade to slightly
		// stale data rather than a thinner certificate.
		s.applyCached(ctx, identity, userID)
		return
	}

	applyLDAPValues(identity, entry.Values)

	if err := s.persist(ctx, userID, entry); err != nil {
		s.log.ErrorContext(ctx, "failed to persist directory enrichment",
			"subject", identity.Subject, "error", err)
	}
}

// lookup dials the directory and resolves identity's entry through the
// shared anchor walk, then runs each field's searches.
//
// row is the caller's stored bookkeeping, or nil for a user who has never
// been enriched — a first login, or the first pass after ldap.id_attribute
// was configured. Nil simply means there are no anchors yet and the walk
// starts at the filter.
func (s *LDAPService) lookup(ctx context.Context, identity *Identity, row *model.UserLDAP) (*ldapEntry, error) {
	conn, err := s.dial(&s.config.LDAP)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the directory connection", "error", err)
		}
	}()

	entry, outcome := s.resolveAnchored(ctx, conn, identity, row)
	switch outcome {
	case syncFound:
		return entry, nil
	case syncMissing:
		return nil, errors.New("no directory entry matched")
	default:
		return nil, errors.New("the directory could not be read")
	}
}

// resolveAnchored finds identity's directory entry: by unique ID first,
// then by the stored DN, then by one rendered user_filter search.
//
// The order is most-stable-first. The ID (ldap.id_attribute) is the only
// anchor that does not move — a person moved between OUs gets a new DN, and
// a person renamed stops matching a user_filter keyed on their username —
// so without it a rename is indistinguishable from a deletion and walks the
// account toward the sync.disable_after auto-disable. The DN is second
// because it is a single base-object read and it distinguishes "entry
// deleted" from "filter no longer matches". The filter is last because it
// is the only step that can find an entry both stored anchors have lost.
//
// Every step is skipped when it has nothing to go on, so a deployment with
// no ldap.id_attribute and a nil row walks straight to the filter and
// behaves exactly as it did before any of this existed.
//
// The three outcomes are distinct on purpose: syncMissing is reserved for a
// search that *succeeded* and found nothing, since that is the only result
// that may ever count against a user. Every failure is syncFailed, so a
// directory that is merely unreachable can never disable anyone.
//
// row may be nil, meaning no anchors are stored yet.
func (s *LDAPService) resolveAnchored(ctx context.Context, conn ldapConn, identity *Identity, row *model.UserLDAP) (*ldapEntry, syncOutcome) {
	logAttrs := []any{"subject", identity.Subject, "username", identity.Username}

	if row != nil {
		if filter := directoryIDFilter(s.config.LDAP.IDAttribute, row.DirectoryID); filter != "" {
			entry, err := s.searchOne(conn, s.config.LDAP.BaseDN, filter, s.primaryAttrs)
			switch {
			case err != nil:
				// Includes two entries sharing an ID, which is a directory
				// problem rather than a missing person: it must not count
				// as a miss, so it falls through to the next anchor like
				// any other failed step.
				s.log.WarnContext(ctx, "directory read by unique id failed; falling back to the stored DN",
					append(logAttrs, "error", err)...)
			case entry != nil:
				if entry.DN != row.DN {
					// The whole payoff: the entry moved or was renamed and
					// was found anyway. The new DN is persisted with the
					// read, so the cheaper anchor is correct again next
					// time.
					s.log.InfoContext(ctx, "directory entry re-anchored by unique id",
						append(logAttrs, "old_dn", row.DN, "new_dn", entry.DN)...)
				}
				return s.finishResolve(ctx, conn, identity, entry, logAttrs)
			}
		}

		if row.DN != "" {
			entry, err := s.readByDN(conn, row.DN)
			switch {
			case err != nil:
				s.log.WarnContext(ctx, "directory read by DN failed; falling back to a filter search",
					append(logAttrs, "dn", row.DN, "error", err)...)
			case entry != nil:
				return s.finishResolve(ctx, conn, identity, entry, logAttrs)
			}
		}
	}

	filter, err := s.userFilter.execute(s.filterData(identity, "", nil))
	if err != nil {
		s.log.ErrorContext(ctx, "failed to render the user filter",
			append(logAttrs, "error", err)...)
		return nil, syncFailed
	}
	entry, err := s.searchOne(conn, s.config.LDAP.BaseDN, filter, s.primaryAttrs)
	if err != nil {
		s.log.WarnContext(ctx, "directory search failed; nothing counted as a miss",
			append(logAttrs, "error", err)...)
		return nil, syncFailed
	}
	if entry == nil {
		// The search succeeded and found nothing. This, and only this, is
		// a miss.
		return nil, syncMissing
	}
	return s.finishResolve(ctx, conn, identity, entry, logAttrs)
}

// finishResolve runs the field searches over a located entry. A failure
// here keeps the caller's cached values rather than returning a thinner
// record: a transient error must never shrink a principal list.
func (s *LDAPService) finishResolve(ctx context.Context, conn ldapConn, identity *Identity, entry *ldap.Entry, logAttrs []any) (*ldapEntry, syncOutcome) {
	resolved, err := s.resolveFields(ctx, conn, identity, entry)
	if err != nil {
		s.log.WarnContext(ctx, "directory field searches failed; keeping the cached values",
			append(logAttrs, "error", err)...)
		return nil, syncFailed
	}
	return resolved, syncFound
}

// resolveFields collects each field's values from the primary entry and
// from its searches.
func (s *LDAPService) resolveFields(ctx context.Context, conn ldapConn, identity *Identity, entry *ldap.Entry) (*ldapEntry, error) {
	out := &ldapEntry{
		DN:          entry.DN,
		DirectoryID: directoryIDValue(entry, s.config.LDAP.IDAttribute),
		Values:      map[string][]string{},
		Attrs:       map[string]string{},
	}
	for _, a := range s.primaryAttrs {
		out.Attrs[a] = entry.GetAttributeValue(a)
	}

	limits := s.config.LDAP.Limits
	for _, field := range s.fields {
		var values []string
		if field.attribute != "" {
			values = append(values, s.capValues(ctx, field.name, entry.GetAttributeValues(field.attribute))...)
		}

		for _, search := range field.searches {
			filter, err := search.filter.execute(s.filterData(identity, entry.DN, out.Attrs))
			if err != nil {
				// A search whose filter cannot render is a config problem,
				// not a transient one, so it is reported rather than
				// silently skipped.
				return nil, fmt.Errorf("%s: %w", search.name, err)
			}
			result, err := s.search(conn, search.baseDN, filter, []string{search.value}, limits.MaxEntriesPerSearch)
			if err != nil {
				// A single failed search must not shrink the field: a
				// transient error narrowing a principal list is worse than
				// keeping what was already known. The caller's cached
				// values survive because the field is left short here and
				// the merge is per field, not per value.
				return nil, fmt.Errorf("%s: %w", search.name, err)
			}
			for _, e := range result.Entries {
				values = append(values, e.GetAttributeValues(search.value)...)
			}
		}

		out.Values[field.name] = dedupe(values)
	}
	return out, nil
}

// capValues truncates a multi-valued attribute at the configured limit,
// logging the truncation so a deployment that hits one can see it.
func (s *LDAPService) capValues(ctx context.Context, field string, values []string) []string {
	limit := s.config.LDAP.Limits.MaxValuesPerAttribute
	if limit <= 0 || len(values) <= limit {
		return values
	}
	s.log.WarnContext(ctx, "directory attribute exceeded the configured value cap and was truncated",
		"field", field, "values", len(values), "limit", limit)
	return values[:limit]
}

// filterData assembles what a filter template renders against.
func (s *LDAPService) filterData(identity *Identity, dn string, attrs map[string]string) filterData {
	extra := map[string]string{}
	for name, v := range identity.Extra {
		if scalar, ok := v.Scalar(); ok {
			extra[name] = scalar
		}
	}
	return filterData{
		Username: identity.Username,
		Email:    identity.Email,
		Subject:  identity.Subject,
		DN:       dn,
		Extra:    extra,
		Attr:     attrs,
	}
}

// searchOne runs a search expecting at most one entry, returning nil when
// nothing matched. More than one match is ambiguous — the filter names a
// person — so it is an error rather than an arbitrary pick.
func (s *LDAPService) searchOne(conn ldapConn, baseDN, filter string, attrs []string) (*ldap.Entry, error) {
	result, err := s.search(conn, baseDN, filter, attrs, 2)
	if err != nil {
		return nil, err
	}
	switch len(result.Entries) {
	case 0:
		return nil, nil
	case 1:
		return result.Entries[0], nil
	default:
		return nil, fmt.Errorf("the directory filter %s matched more than one entry", filter)
	}
}

// search runs one bounded search.
func (s *LDAPService) search(conn ldapConn, baseDN, filter string, attrs []string, sizeLimit int) (*ldap.SearchResult, error) {
	req := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		sizeLimit, int(s.config.LDAP.Timeout.Seconds()), false,
		filter, attrs, nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		// A size-limit hit is not a failure: it means the cap did its job,
		// and the entries already returned are usable.
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) && result != nil {
			s.log.Warn("directory search hit the configured size limit and was truncated",
				"filter", filter, "limit", sizeLimit)
			return result, nil
		}
		return nil, fmt.Errorf("directory search failed: %w", err)
	}
	return result, nil
}

// applyValues merges resolved directory values onto identity, per field.
//
// The rule: a configured LDAP field wins over the OIDC value; an
// unconfigured one leaves it untouched. Override rather than union, because
// union makes it impossible to retire a stale principal from only one
// source. Groups are the exception — the session identity's Groups stays
// the OIDC claim, and LDAP groups are persisted alongside instead.
func applyLDAPValues(identity *Identity, values map[string][]string) {
	for name, vals := range values {
		switch name {
		case config.LDAPFieldOtherAccounts:
			identity.OtherAccounts = vals
		case config.LDAPFieldServiceAccounts:
			identity.ServiceAccounts = vals
		case config.LDAPFieldName:
			// One name, so only the first value is taken. A directory that
			// carries several (cn is routinely multi-valued) is naming the
			// same person more than once, and joining them would produce a
			// label no directory actually holds.
			if len(vals) > 0 {
				identity.DisplayName = vals[0]
			}
		case config.LDAPFieldGroups:
			// Persisted, never merged into the session identity: see the
			// invariant in https://mnestor.github.io/ssoossh/internals/invariants/.
		default:
			// Any other name is an extra template field, on the same
			// contract as OAuthFields.Extra.
			if identity.Extra == nil {
				identity.Extra = map[string]extraValue{}
			}
			if len(vals) == 1 {
				identity.Extra[name] = scalarExtra(vals[0])
			} else {
				identity.Extra[name] = listExtra(vals)
			}
		}
	}
}

// applyCached falls back to the last successful read when the directory is
// unreachable, so an outage costs freshness rather than principals.
func (s *LDAPService) applyCached(ctx context.Context, identity *Identity, userID string) {
	row, err := s.storedValues(ctx, userID)
	if err != nil || row == nil {
		return
	}
	applyLDAPValues(identity, row.values)
	s.log.InfoContext(ctx, "used cached directory attributes after a failed lookup",
		"subject", identity.Subject, "last_seen_at", row.lastSeenAt)
}

// applyStored overlays the stored directory values on identity, which is
// what makes a live session reflect a sync that ran after its login.
//
// A nil receiver — LDAP disabled — is a no-op, leaving the OIDC values the
// caller already loaded as the whole answer. Nothing here is an error the
// caller must act on: no bookkeeping row means the user has never been
// enriched, and a read that fails leaves the identity untouched.
func (s *LDAPService) applyStored(ctx context.Context, identity *Identity, userID string) error {
	if s == nil || identity == nil || userID == "" {
		return nil
	}
	row, err := s.storedValues(ctx, userID)
	if err != nil || row == nil {
		return err
	}
	applyLDAPValues(identity, row.values)
	return nil
}

// storedLDAPValues is one decoded user_ldap row: the field values and when
// the entry behind them was last actually read.
type storedLDAPValues struct {
	values     map[string][]string
	lastSeenAt *time.Time
}

// storedRow reads one user's bookkeeping row. Returns nil, nil when there
// is none, which is a user who has never been enriched rather than an
// error.
func (s *LDAPService) storedRow(ctx context.Context, userID string) (*model.UserLDAP, error) {
	if userID == "" {
		return nil, nil
	}
	var row model.UserLDAP
	if err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the directory bookkeeping row: %w", err)
	}
	return &row, nil
}

// storedValues reads and decodes one user's cached directory values.
// Returns nil, nil when there is no bookkeeping row or its JSON is
// unreadable — both mean "nothing to overlay", which is the same outcome
// for every caller.
func (s *LDAPService) storedValues(ctx context.Context, userID string) (*storedLDAPValues, error) {
	row, err := s.storedRow(ctx, userID)
	if err != nil {
		s.log.WarnContext(ctx, "failed to read cached directory attributes", "error", err)
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	var values map[string][]string
	if err := json.Unmarshal([]byte(row.Attributes), &values); err != nil {
		s.log.WarnContext(ctx, "failed to decode cached directory attributes", "error", err)
		return nil, nil
	}
	return &storedLDAPValues{values: values, lastSeenAt: row.LastSeenAt}, nil
}

// persist writes the sync bookkeeping row and replaces the user's
// LDAP-sourced group rows.
func (s *LDAPService) persist(ctx context.Context, userID string, entry *ldapEntry) error {
	encoded, err := json.Marshal(entry.Values)
	if err != nil {
		// not covered: Values is a map of string slices, so json.Marshal
		// cannot fail on it.
		return fmt.Errorf("failed to encode directory attributes: %w", err)
	}
	if cap := s.config.LDAP.Limits.MaxAttributesBytes; cap > 0 && len(encoded) > cap {
		return fmt.Errorf("directory attributes for user %s are %d bytes, over the %d byte cap", userID, len(encoded), cap)
	}

	now := time.Now()
	row := model.UserLDAP{
		UserID:            userID,
		DirectoryID:       entry.DirectoryID,
		DN:                entry.DN,
		Attributes:        string(encoded),
		LastSeenAt:        &now,
		LastSyncedAt:      &now,
		ConsecutiveMisses: 0,
		// A find closes the missing window outright. Nothing decays it
		// gradually: the threshold measures one unbroken absence, so an
		// entry that comes back and disappears again starts a fresh one.
		FirstMissingAt: nil,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"directory_id", "dn", "attributes", "last_seen_at", "last_synced_at", "consecutive_misses", "first_missing_at", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("failed to persist directory bookkeeping: %w", err)
		}
		return replaceGroups(tx, userID, model.GroupSourceLDAP, s.allowed(entry.Values[config.LDAPFieldGroups]), now)
	})
}

// allowed reduces directory group values to comparable names and keeps only
// those the configuration references.
func (s *LDAPService) allowed(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		name := reduceGroupName(v)
		if slices.Contains(s.groupAllowlist, name) {
			out = append(out, name)
		}
	}
	return dedupe(out)
}

// replaceGroups rewrites one source's group rows for a user: delete then
// insert, so a membership that disappeared is actually gone rather than
// lingering. FirstSeenAt is preserved for rows that persist across the
// replace, since "since when" is the part worth keeping.
func replaceGroups(tx *gorm.DB, userID string, source model.GroupSource, names []string, now time.Time) error {
	var existing []model.UserGroup
	if err := tx.Where("user_id = ? AND source = ?", userID, source).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to read existing group rows: %w", err)
	}
	firstSeen := map[string]time.Time{}
	for _, row := range existing {
		firstSeen[row.GroupName] = row.FirstSeenAt
	}

	if err := tx.Where("user_id = ? AND source = ?", userID, source).
		Delete(&model.UserGroup{}).Error; err != nil {
		return fmt.Errorf("failed to clear existing group rows: %w", err)
	}
	if len(names) == 0 {
		return nil
	}

	rows := make([]model.UserGroup, 0, len(names))
	for _, name := range names {
		seen, ok := firstSeen[name]
		if !ok {
			seen = now
		}
		rows = append(rows, model.UserGroup{
			ID:          uuid.NewString(),
			UserID:      userID,
			GroupName:   name,
			Source:      source,
			FirstSeenAt: seen,
			LastSeenAt:  now,
		})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("failed to write group rows: %w", err)
	}
	return nil
}

// dedupe removes duplicates while preserving first-seen order.
func dedupe(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
