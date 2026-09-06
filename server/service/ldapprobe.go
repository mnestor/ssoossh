package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Probe limits. The point of the feature is asking a directory for the whole
// entry, so it needs bounds of its own rather than the config's — those cap
// what reaches the database, and a probe writes nothing to the database.
const (
	// probeMaxEntries is how many matches a probe reports. The login path
	// refuses anything but one; more than that is reported so an operator
	// can see the filter is too loose rather than only that it failed.
	probeMaxEntries = 5

	// probeMaxValuesPerAttribute caps the values rendered for one
	// attribute. A group-heavy entry can carry thousands of memberOf
	// values, and the truncation is reported.
	probeMaxValuesPerAttribute = 200

	// probeMaxValueBytes caps one rendered value. Directories carry
	// certificates and photos as attributes; a probe says how long the
	// value was rather than posting it into a browser.
	probeMaxValueBytes = 4096
)

// LDAPDiagnostics is the directory-diagnostics surface the admin HTTP
// handlers drive: one read-only probe, the sync, and the record of the last
// pass. Implemented by *LDAPService.
//
// An interface here rather than the concrete service because the handlers'
// own logic — authorization, binding resolution, the wire mapping, the
// conflict and the audit record — is worth testing without a directory, and
// the alternative is a fake LDAP server in the controller package.
type LDAPDiagnostics interface {
	Probe(ctx context.Context, req ProbeRequest) (*ProbeResult, error)
	SyncWithOptions(ctx context.Context, opts SyncOptions) (*model.LDAPSyncRun, error)
	LastSyncRun(ctx context.Context) (*model.LDAPSyncRun, error)
	SyncRunning() bool
}

// ProbeFilterMode says how the probe's filter text is turned into the string
// sent to the directory.
type ProbeFilterMode string

const (
	// ProbeFilterTemplate renders the text as a Go template against the
	// bindings, through the same RFC 4515 escaping the login path uses.
	// This is the mode that answers "what does my configured filter
	// actually send".
	ProbeFilterTemplate ProbeFilterMode = "template"

	// ProbeFilterLiteral sends the text exactly as typed. Nothing is
	// interpolated, so nothing is escaped: escaping a filter an operator
	// wrote by hand would corrupt it.
	ProbeFilterLiteral ProbeFilterMode = "literal"
)

// ProbeBindings is the identity a template-mode filter renders against —
// the same fields a login renders against, supplied directly so an admin can
// test an entry before that person has ever logged in.
type ProbeBindings struct {
	Username string
	Email    string
	Subject  string
	Extra    map[string]string
}

// ProbeRequest is one probe.
//
// There is deliberately no connection here. The probe always uses the
// running ldap.url, bind credentials and base_dn: an admin-supplied target
// would turn the server's bind password into a credential-exfiltration
// primitive and the server itself into an outbound-connection primitive.
// What an operator can vary is the question, not who is asked.
type ProbeRequest struct {
	Bindings ProbeBindings

	// Mode selects template or literal handling of Filter.
	Mode ProbeFilterMode

	// Filter is the filter to run. Empty means the configured
	// ldap.user_filter, which is the common case: "what does my config do
	// for this person".
	Filter string

	// Attributes are the attribute names to request. Empty requests every
	// user attribute, which is the point of the console — you cannot map
	// the field you should have mapped if you can only see the six the
	// config already names.
	Attributes []string
}

// ProbeResult is everything one probe learned, in the three stages the login
// path already runs: the entry as the directory returned it, the field
// mapping resolved from it, and the merge and allowlist that would follow.
//
// Nothing here was written. There is no persist call on this path at all: no
// user_ldap row, no group rows, no miss counters, no auto-disable.
type ProbeResult struct {
	// BaseDN, FilterSent and Attributes are the request as it went out, so
	// an operator can see the rendered filter rather than the template.
	BaseDN     string
	FilterSent string
	Attributes []string

	// Mode echoes how FilterSent was produced, since the escaping only
	// happens in one of them.
	Mode ProbeFilterMode

	// Matched is how many entries the filter found, capped at
	// probeMaxEntries. The login path refuses anything but exactly one.
	Matched int

	// Entry is the first match, with every attribute the directory
	// returned. Nil when nothing matched.
	Entry *ProbeEntry

	// IDAttribute echoes ldap.id_attribute, and DirectoryID is what it
	// resolved to on this entry — the value that would be stored as the
	// re-anchoring identifier. Both empty when it is unconfigured, which is
	// the case probeSuggestions offers a candidate for.
	IDAttribute string
	DirectoryID string

	// Fields is the field-mapping stage: what each configured
	// ldap.fields entry resolved to, and why.
	Fields []ProbeField

	// Merge is the merge-and-allowlist stage: what would have been
	// persisted, what would have been dropped, and what would have
	// overridden an OIDC value.
	Merge []ProbeMerge

	// Suggestions are config lines that would map something the probe
	// found and the configuration ignores. Suggestions, not decisions: the
	// operator still writes and reviews the change.
	Suggestions []ProbeSuggestion

	// Elapsed is how long the directory operations took, and Timeout the
	// bound they ran under (ldap.timeout).
	Elapsed time.Duration
	Timeout time.Duration

	// TLSInsecureSkipVerify reports that the connection did not verify the
	// directory's certificate. A probe that succeeds only because
	// verification was off has to say so.
	TLSInsecureSkipVerify bool
}

// ProbeEntry is one directory entry as returned, before any mapping.
type ProbeEntry struct {
	DN string
	// Attributes are every attribute the directory sent back, sorted by
	// name so two probes read the same way.
	Attributes []ProbeAttribute
}

// ProbeAttribute is one attribute of the returned entry.
type ProbeAttribute struct {
	Name   string
	Values []string
	// Configured reports that some ldap.fields entry names this attribute,
	// which is what the console highlights.
	Configured bool
	// TruncatedValues is how many values were dropped from Values by the
	// probe's own cap; zero when nothing was.
	TruncatedValues int
}

// ProbeField is one configured field's resolution.
type ProbeField struct {
	Name string
	// Attribute is the entry attribute the field reads, empty when the
	// field is search-only.
	Attribute string
	// AttributeValues are the values that attribute contributed.
	AttributeValues []string
	// AttributePresent reports whether the attribute was on the entry at
	// all, which distinguishes "empty" from "misspelled".
	AttributePresent bool
	// Searches is each secondary search under the field.
	Searches []ProbeSearch
	// Values is the field's resolved value list, deduped, as the login
	// path would compute it.
	Values []string
	// Error is a search or template failure, reported rather than fatal:
	// one broken field should not hide the rest of the answer.
	Error string
}

// ProbeSearch is one secondary search's contribution to a field.
type ProbeSearch struct {
	Name string
	// BaseDN and FilterSent are what actually went to the directory,
	// rendered and escaped.
	BaseDN     string
	FilterSent string
	// Value is the attribute read off each matched entry.
	Value string
	// Entries is how many entries matched, and Values what they
	// contributed.
	Entries int
	Values  []string
	Error   string
}

// ProbeMerge is one field's fate at the merge: what the login path would do
// with the resolved values.
type ProbeMerge struct {
	Name string
	// Action is what the merge rule does with this field: "override" for a
	// field that replaces its OIDC value, "persist-groups" for the group
	// field, which is stored rather than merged into the session identity,
	// or "extra" for a template field.
	Action string
	// Kept and Dropped split the values by the group allowlist. Dropped is
	// only ever non-empty for the group field: nothing else is filtered.
	Kept    []string
	Dropped []string
	// Note explains the action in the terms the operator configured it in.
	Note string
}

// ProbeSuggestion is a config line that would map something the probe found.
type ProbeSuggestion struct {
	// Reason says what was found and why it is currently ignored.
	Reason string
	// YAML is the block to add, ready to paste and review.
	YAML string
}

// Probe runs one read-only directory lookup and reports it in full.
//
// It is the same three stages as a login — lookup, field resolution, then
// the merge and allowlist — stopped before the write. persist is not reached
// from here by any input, which is the guarantee the console prints and a
// test asserts.
//
// Returns an error only when the probe could not be run at all: LDAP
// disabled, an unparseable filter, or a directory that could not be reached.
// A filter that matched nothing is a successful probe with an answer.
func (s *LDAPService) Probe(ctx context.Context, req ProbeRequest) (*ProbeResult, error) {
	if s == nil {
		return nil, fmt.Errorf("directory enrichment is disabled, so there is nothing to probe")
	}

	filter, err := s.probeFilter(req)
	if err != nil {
		return nil, err
	}

	attrs := req.Attributes
	if len(attrs) == 0 {
		// "*" is the LDAP spelling for every user attribute, which is the
		// whole point: an operator cannot map a field they cannot see.
		//
		// The identifier attributes are named individually because they are
		// operational and "*" does not return them. Asking for a handful
		// that a given directory will not have costs nothing — an absent
		// attribute is simply absent from the result — and it is what lets
		// the probe answer "which attribute do I anchor on", which is not a
		// question an operator can answer from their own directory's
		// documentation in any reasonable time.
		attrs = append([]string{"*"}, probeIDAttributes(s.config.LDAP.IDAttribute)...)
	}

	result := &ProbeResult{
		BaseDN:                s.config.LDAP.BaseDN,
		FilterSent:            filter,
		Attributes:            attrs,
		Mode:                  req.Mode,
		Timeout:               s.config.LDAP.Timeout,
		TLSInsecureSkipVerify: s.config.LDAP.TLSInsecureSkipVerify,
	}

	started := time.Now()
	conn, err := s.dial(&s.config.LDAP)
	if err != nil {
		return nil, fmt.Errorf("could not reach the directory: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the probe's directory connection", "error", err)
		}
	}()

	search, err := s.search(conn, s.config.LDAP.BaseDN, filter, attrs, probeMaxEntries)
	if err != nil {
		return nil, err
	}
	result.Matched = len(search.Entries)
	if result.Matched == 0 {
		result.Elapsed = time.Since(started)
		return result, nil
	}

	entry := search.Entries[0]
	result.Entry = s.probeEntry(entry)
	result.IDAttribute = s.config.LDAP.IDAttribute
	result.DirectoryID = directoryIDValue(entry, s.config.LDAP.IDAttribute)
	result.Fields = s.probeFields(conn, req, entry)
	result.Merge = s.probeMerge(result.Fields)
	result.Suggestions = s.probeSuggestions(result)
	result.Elapsed = time.Since(started)
	return result, nil
}

// probeFilter produces the exact string to send. Template mode renders
// through the same escaping the login path uses, which is the only way to
// see what a configured filter really sends; literal mode sends the typed
// string untouched, because nothing is being interpolated and escaping a
// hand-written filter would corrupt it.
func (s *LDAPService) probeFilter(req ProbeRequest) (string, error) {
	text := strings.TrimSpace(req.Filter)

	if req.Mode == ProbeFilterLiteral {
		if text == "" {
			return "", fmt.Errorf("a literal probe needs a filter to send")
		}
		return text, nil
	}

	tmpl := s.userFilter
	if text != "" {
		parsed, err := parseFilterTemplate("probe", text)
		if err != nil {
			return "", err
		}
		tmpl = parsed
	}
	if tmpl == nil {
		// not covered: NewLDAPService parses ldap.user_filter at
		// construction and fails startup without one, so a service with a
		// nil template cannot exist.
		return "", fmt.Errorf("no filter to run: ldap.user_filter is unset and none was supplied")
	}

	return tmpl.execute(filterData{
		Username: req.Bindings.Username,
		Email:    req.Bindings.Email,
		Subject:  req.Bindings.Subject,
		Extra:    req.Bindings.Extra,
	})
}

// probeEntry renders the returned entry in full, marking the attributes the
// configuration names and capping what a browser has to hold.
func (s *LDAPService) probeEntry(entry *ldap.Entry) *ProbeEntry {
	out := &ProbeEntry{DN: entry.DN}
	for _, attr := range entry.Attributes {
		values, dropped := capProbeValues(attr.Values)
		out.Attributes = append(out.Attributes, ProbeAttribute{
			Name:            attr.Name,
			Values:          values,
			Configured:      s.namesAttribute(attr.Name),
			TruncatedValues: dropped,
		})
	}
	slices.SortFunc(out.Attributes, func(a, b ProbeAttribute) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// namesAttribute reports whether any configured field reads this attribute,
// matched case-insensitively because directory attribute names are.
func (s *LDAPService) namesAttribute(name string) bool {
	for _, field := range s.fields {
		if strings.EqualFold(field.attribute, name) {
			return true
		}
	}
	return slices.ContainsFunc(s.primaryAttrs, func(a string) bool {
		return strings.EqualFold(a, name)
	})
}

// probeFields runs the field-resolution stage, recording each source's
// contribution instead of only its result.
//
// A field that fails records its error and the rest still resolve: the login
// path stops at the first failure because a half-resolved identity is worse
// than a cached one, but a diagnostic that stops at the first problem hides
// the others.
func (s *LDAPService) probeFields(conn ldapConn, req ProbeRequest, entry *ldap.Entry) []ProbeField {
	attrs := map[string]string{}
	for _, a := range s.primaryAttrs {
		attrs[a] = entry.GetAttributeValue(a)
	}
	data := filterData{
		Username: req.Bindings.Username,
		Email:    req.Bindings.Email,
		Subject:  req.Bindings.Subject,
		DN:       entry.DN,
		Extra:    req.Bindings.Extra,
		Attr:     attrs,
	}

	out := make([]ProbeField, 0, len(s.fields))
	for _, field := range s.fields {
		resolved := ProbeField{Name: field.name, Attribute: field.attribute}
		var values []string

		if field.attribute != "" {
			resolved.AttributePresent = slices.ContainsFunc(entry.Attributes, func(a *ldap.EntryAttribute) bool {
				return strings.EqualFold(a.Name, field.attribute)
			})
			resolved.AttributeValues, _ = capProbeValues(entry.GetAttributeValues(field.attribute))
			values = append(values, resolved.AttributeValues...)
		}

		for _, search := range field.searches {
			ran := ProbeSearch{Name: search.name, BaseDN: search.baseDN, Value: search.value}
			filter, err := search.filter.execute(data)
			if err != nil {
				ran.Error = err.Error()
				resolved.Searches = append(resolved.Searches, ran)
				continue
			}
			ran.FilterSent = filter

			result, err := s.search(conn, search.baseDN, filter, []string{search.value}, s.config.LDAP.Limits.MaxEntriesPerSearch)
			if err != nil {
				ran.Error = err.Error()
				resolved.Searches = append(resolved.Searches, ran)
				continue
			}
			ran.Entries = len(result.Entries)
			for _, e := range result.Entries {
				ran.Values = append(ran.Values, e.GetAttributeValues(search.value)...)
			}
			ran.Values, _ = capProbeValues(ran.Values)
			values = append(values, ran.Values...)
			resolved.Searches = append(resolved.Searches, ran)
		}

		resolved.Values = dedupe(values)
		out = append(out, resolved)
	}
	return out
}

// probeMerge reports what the merge would do with each resolved field: the
// rule is a configured field overriding its OIDC value, groups being
// persisted through the allowlist rather than merged into the identity, and
// anything else landing as an extra template field.
func (s *LDAPService) probeMerge(fields []ProbeField) []ProbeMerge {
	out := make([]ProbeMerge, 0, len(fields))
	for _, field := range fields {
		merged := ProbeMerge{Name: field.Name, Kept: field.Values}
		switch field.Name {
		case config.LDAPFieldGroups:
			merged.Action = "persist-groups"
			merged.Kept = nil
			for _, value := range field.Values {
				name := reduceGroupName(value)
				if slices.Contains(s.groupAllowlist, name) {
					merged.Kept = append(merged.Kept, name)
					continue
				}
				merged.Dropped = append(merged.Dropped, name)
			}
			merged.Note = "stored as user_groups rows, never merged into the session identity. Only names the configuration references are kept."
		case config.LDAPFieldOtherAccounts, config.LDAPFieldServiceAccounts:
			merged.Action = "override"
			merged.Note = "replaces the OIDC value outright rather than merging with it, so a retired account really goes away."
		case config.LDAPFieldName:
			merged.Action = "override"
			if len(merged.Kept) > 1 {
				// A multi-valued cn names the same person twice; only the
				// first is taken, and saying so beats an operator wondering
				// which one the UI picked.
				merged.Kept = merged.Kept[:1]
			}
			merged.Note = "replaces the OIDC name claim. Display only: shown in the web UI and offered to email templates, never a principal, a key ID input, or an authorization input. Only the first value is used."
		default:
			merged.Action = "extra"
			merged.Note = fmt.Sprintf("lands as an extra field, reachable from a key ID template as {{.Extra.%s}}.", field.Name)
		}
		out = append(out, merged)
	}
	return out
}

// probeSuggestions turns what the probe found and the configuration ignores
// into config lines. Suggestions only: they say what would keep a value, not
// that keeping it is right.
func (s *LDAPService) probeSuggestions(result *ProbeResult) []ProbeSuggestion {
	var out []ProbeSuggestion
	if result.Entry == nil {
		return nil
	}

	// Attributes on the entry that no field reads. Operational attributes
	// are skipped: they are directory bookkeeping, not identity.
	for _, attr := range result.Entry.Attributes {
		if attr.Configured || isOperationalAttribute(attr.Name) {
			continue
		}
		out = append(out, ProbeSuggestion{
			Reason: fmt.Sprintf("%s is on the entry but no configured field reads it", attr.Name),
			YAML: fmt.Sprintf("ldap:\n  fields:\n    %s: %s",
				suggestedFieldName(attr.Name), attr.Name),
		})
	}

	// The re-anchoring identifier, which is the suggestion with the most
	// leverage in the whole console: without one, a person renamed in the
	// directory stops matching user_filter, looks exactly like a deleted
	// entry, and is walked toward the auto-disable. Every directory has an
	// identifier attribute and every directory calls it something else, so
	// naming the one this directory actually returned is the answer an
	// operator cannot easily look up.
	if s.config.LDAP.IDAttribute == "" {
		for _, candidate := range wellKnownIDAttributes {
			attr := findProbeAttribute(result.Entry, candidate)
			if attr == nil {
				continue
			}
			out = append(out, ProbeSuggestion{
				Reason: fmt.Sprintf("%s is on this entry and ldap.id_attribute is unset: without it, a renamed or moved entry cannot be re-anchored and counts toward the auto-disable", attr.Name),
				YAML:   fmt.Sprintf("ldap:\n  id_attribute: %s", attr.Name),
			})
			break
		}
	}

	// Group values the allowlist discards. The allowlist is why an
	// operator cannot see the group they forgot to configure, so naming
	// the ones that were dropped is most of the value of the whole
	// console.
	for _, merge := range result.Merge {
		if merge.Action != "persist-groups" || len(merge.Dropped) == 0 {
			continue
		}
		quoted := make([]string, 0, len(merge.Dropped))
		for _, name := range merge.Dropped {
			quoted = append(quoted, fmt.Sprintf("%q", name))
		}
		out = append(out, ProbeSuggestion{
			Reason: fmt.Sprintf("%d group membership(s) were dropped: no configured group name matches them", len(merge.Dropped)),
			YAML:   fmt.Sprintf("ldap:\n  sync:\n    extra_groups: [%s]", strings.Join(quoted, ", ")),
		})
	}

	return out
}

// wellKnownIDAttributes are the unique, immutable identifier attributes the
// common directories use, in the order the probe offers them. Every
// directory has one and every directory names it differently: entryUUID is
// the RFC 4530 standard that OpenLDAP and 389 Directory Server implement,
// objectGUID is Active Directory's, ipaUniqueID is FreeIPA's, and
// nsuniqueid is the older 389/Netscape one that predates entryUUID.
var wellKnownIDAttributes = []string{"entryUUID", "objectGUID", "ipaUniqueID", "nsuniqueid"}

// probeIDAttributes is the identifier attributes to request explicitly:
// every well-known one, plus the configured attribute when it is something
// else. Operational attributes are not returned by "*", so an unrequested
// one is simply invisible.
func probeIDAttributes(configured string) []string {
	out := append([]string{}, wellKnownIDAttributes...)
	if configured != "" && !slices.ContainsFunc(out, func(a string) bool {
		return strings.EqualFold(a, configured)
	}) {
		out = append(out, configured)
	}
	return out
}

// findProbeAttribute looks up one attribute on a probed entry, matching the
// way a directory does: case-insensitively. Returns nil when absent.
func findProbeAttribute(entry *ProbeEntry, name string) *ProbeAttribute {
	if entry == nil {
		return nil
	}
	for i := range entry.Attributes {
		if strings.EqualFold(entry.Attributes[i].Name, name) {
			return &entry.Attributes[i]
		}
	}
	return nil
}

// operationalAttributes are directory bookkeeping rather than identity, so
// suggesting a mapping for them would be noise. Matched case-insensitively.
// The identifier attributes are here too: they are exactly the thing
// id_attribute takes, and suggesting one be mapped to a template field
// instead would point an operator away from the setting that matters.
var operationalAttributes = []string{
	"objectClass", "createTimestamp", "modifyTimestamp", "creatorsName",
	"modifiersName", "entryUUID", "objectGUID", "ipaUniqueID", "nsuniqueid",
	"entryDN", "entryCSN", "structuralObjectClass",
	"subschemaSubentry", "hasSubordinates", "userPassword",
}

// isOperationalAttribute reports whether name is directory bookkeeping.
func isOperationalAttribute(name string) bool {
	return slices.ContainsFunc(operationalAttributes, func(a string) bool {
		return strings.EqualFold(a, name)
	})
}

// suggestedFieldName turns a directory attribute name into the snake_case
// field name a config block would use — employeeType becomes employee_type.
func suggestedFieldName(attr string) string {
	var out strings.Builder
	for i, r := range attr {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r + ('a' - 'A'))
			continue
		}
		if r == '-' {
			out.WriteByte('_')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// capProbeValues bounds one attribute's rendered values, returning how many
// were dropped. Long individual values are replaced by their length rather
// than truncated silently, since half a certificate reads like a value.
func capProbeValues(values []string) (capped []string, dropped int) {
	if len(values) > probeMaxValuesPerAttribute {
		dropped = len(values) - probeMaxValuesPerAttribute
		values = values[:probeMaxValuesPerAttribute]
	}
	capped = make([]string, 0, len(values))
	for _, v := range values {
		if len(v) > probeMaxValueBytes {
			capped = append(capped, fmt.Sprintf("<%d bytes, too long to display>", len(v)))
			continue
		}
		capped = append(capped, v)
	}
	return capped, dropped
}
