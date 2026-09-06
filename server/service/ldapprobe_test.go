package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// probeFields is the field configuration the probe tests share: one direct
// attribute per reserved destination plus one extra, which is enough to
// exercise every merge branch.
func probeTestFields() map[string]config.LDAPField {
	return map[string]config.LDAPField{
		config.LDAPFieldGroups:          {Attribute: "memberOf"},
		config.LDAPFieldOtherAccounts:   {Attribute: "altSecurityIdentities"},
		config.LDAPFieldServiceAccounts: {Attribute: "owns"},
		"employee_type":                 {Attribute: "employeeType"},
	}
}

// probeTestEntry is a directory entry carrying both mapped and unmapped
// attributes, and both allowlisted and unknown groups.
func probeTestEntry() *ldap.Entry {
	return entry("uid=alice,ou=people,dc=test", map[string][]string{
		"uid":                   {"alice"},
		"cn":                    {"Alice Example"},
		"memberOf":              {"cn=ssh-admins,ou=groups,dc=test", "cn=vpn-legacy,ou=groups,dc=test"},
		"altSecurityIdentities": {"alice.adm"},
		"employeeType":          {"staff"},
		"departmentNumber":      {"4120"},
		"objectClass":           {"person"},
	})
}

// newProbeService builds a probe-ready service over the shared fake.
func newProbeService(t *testing.T) (*LDAPService, *fakeDirectory, *gorm.DB) {
	t.Helper()

	dir := &fakeDirectory{entries: []*ldap.Entry{probeTestEntry()}}
	svc, db := newLDAPTestService(t, ldapTestConfig(probeTestFields()), dir)
	return svc, dir, db
}

// TestProbe_ShouldReportTheFilterItActuallySent covers the distinction the
// console exists for: a template is escaped on the way out, a literal is
// not, and either way the probe reports the string the directory saw.
func TestProbe_ShouldReportTheFilterItActuallySent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      ProbeRequest
		wantSent string
	}{
		{
			name: "should render the configured filter against the bindings",
			req: ProbeRequest{
				Mode:     ProbeFilterTemplate,
				Bindings: ProbeBindings{Username: "alice"},
			},
			wantSent: "(&(objectClass=person)(uid=alice))",
		},
		{
			name: "should escape an interpolated value in template mode",
			req: ProbeRequest{
				Mode:     ProbeFilterTemplate,
				Bindings: ProbeBindings{Username: "o'brien)"},
			},
			wantSent: `(&(objectClass=person)(uid=o'brien\29))`,
		},
		{
			name: "should render a supplied template rather than the configured one",
			req: ProbeRequest{
				Mode:     ProbeFilterTemplate,
				Filter:   "(mail={{.Email}})",
				Bindings: ProbeBindings{Email: "alice@example.com"},
			},
			wantSent: "(mail=alice@example.com)",
		},
		{
			name: "should send a literal filter exactly as typed",
			req: ProbeRequest{
				Mode:   ProbeFilterLiteral,
				Filter: "(&(objectClass=person)(uid=o'brien))",
			},
			wantSent: "(&(objectClass=person)(uid=o'brien))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, _, _ := newProbeService(t)
			got, err := svc.Probe(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Probe() error = %v", err)
			}
			if got.FilterSent != tt.wantSent {
				t.Errorf("filter sent = %q, want %q", got.FilterSent, tt.wantSent)
			}
		})
	}
}

// TestProbe_ShouldReturnEveryAttributeTheDirectoryReturned is the reason a
// CLI probe against the live config is not enough: you cannot map the field
// you should have mapped if you only see the ones already named.
func TestProbe_ShouldReturnEveryAttributeTheDirectoryReturned(t *testing.T) {
	t.Parallel()

	svc, dir, _ := newProbeService(t)
	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if got.Entry == nil {
		t.Fatal("Probe() found no entry")
	}
	if got.Entry.DN != "uid=alice,ou=people,dc=test" {
		t.Errorf("dn = %q, want the matched entry's", got.Entry.DN)
	}

	names := map[string]ProbeAttribute{}
	for _, attr := range got.Entry.Attributes {
		names[attr.Name] = attr
	}
	for _, want := range []string{"uid", "cn", "memberOf", "employeeType", "departmentNumber"} {
		if _, ok := names[want]; !ok {
			t.Errorf("attribute %q is missing from the returned entry", want)
		}
	}
	if !names["memberOf"].Configured {
		t.Error("memberOf should be marked as named by the configuration")
	}
	if names["departmentNumber"].Configured {
		t.Error("departmentNumber is not named by the configuration and should not be marked as such")
	}

	// The request asks for everything, not the configured subset.
	if len(dir.searches) == 0 {
		t.Fatal("the probe issued no search")
	}
}

// TestProbe_ShouldResolveEachConfiguredField covers the mapping stage: what
// each field read, and whether the attribute was on the entry at all —
// which is what separates "empty" from "misspelled".
func TestProbe_ShouldResolveEachConfiguredField(t *testing.T) {
	t.Parallel()

	svc, _, _ := newProbeService(t)
	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	fields := map[string]ProbeField{}
	for _, f := range got.Fields {
		fields[f.Name] = f
	}

	if fields["employee_type"].Values[0] != "staff" {
		t.Errorf("employee_type resolved to %v, want [staff]", fields["employee_type"].Values)
	}
	if !fields["employee_type"].AttributePresent {
		t.Error("employeeType is on the entry and should be reported as present")
	}
	if fields[config.LDAPFieldServiceAccounts].AttributePresent {
		t.Error("owns is not on the entry and should be reported as absent")
	}
	if len(fields[config.LDAPFieldGroups].Values) != 2 {
		t.Errorf("groups resolved to %v, want both memberOf values", fields[config.LDAPFieldGroups].Values)
	}
}

// TestProbe_ShouldSplitGroupsByTheAllowlist is the merge stage: group rows
// are the only values the allowlist filters, and the dropped ones are what
// an operator cannot otherwise see.
func TestProbe_ShouldSplitGroupsByTheAllowlist(t *testing.T) {
	t.Parallel()

	svc, _, _ := newProbeService(t)
	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	merges := map[string]ProbeMerge{}
	for _, m := range got.Merge {
		merges[m.Name] = m
	}

	groups := merges[config.LDAPFieldGroups]
	if groups.Action != "persist-groups" {
		t.Errorf("groups action = %q, want persist-groups", groups.Action)
	}
	// ssh-admins is admin.require_group in the test config; vpn-legacy is
	// referenced by nothing.
	if len(groups.Kept) != 1 || groups.Kept[0] != "ssh-admins" {
		t.Errorf("kept groups = %v, want [ssh-admins]", groups.Kept)
	}
	if len(groups.Dropped) != 1 || groups.Dropped[0] != "vpn-legacy" {
		t.Errorf("dropped groups = %v, want [vpn-legacy]", groups.Dropped)
	}

	if merges[config.LDAPFieldOtherAccounts].Action != "override" {
		t.Errorf("other_accounts action = %q, want override", merges[config.LDAPFieldOtherAccounts].Action)
	}
	if merges["employee_type"].Action != "extra" {
		t.Errorf("employee_type action = %q, want extra", merges["employee_type"].Action)
	}
}

// TestProbe_ShouldSuggestConfigForWhatItFound checks the suggestions: an
// unmapped attribute and a dropped group each get the block that would keep
// them.
func TestProbe_ShouldSuggestConfigForWhatItFound(t *testing.T) {
	t.Parallel()

	svc, _, _ := newProbeService(t)
	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	var sawAttribute, sawGroup, sawOperational bool
	for _, s := range got.Suggestions {
		switch {
		case strings.Contains(s.YAML, "department_number: departmentNumber"):
			sawAttribute = true
		case strings.Contains(s.YAML, `extra_groups: ["vpn-legacy"]`):
			sawGroup = true
		case strings.Contains(s.Reason, "objectClass"):
			sawOperational = true
		}
	}

	if !sawAttribute {
		t.Errorf("no suggestion mapped the unmapped departmentNumber attribute: %+v", got.Suggestions)
	}
	if !sawGroup {
		t.Errorf("no suggestion offered to keep the dropped group: %+v", got.Suggestions)
	}
	if sawOperational {
		t.Error("objectClass is directory bookkeeping and should not be suggested as a field")
	}
}

// TestProbe_ShouldReportNoMatchAsAnAnswer keeps "the filter found nothing"
// from reading as "the probe failed": it is the most common thing an
// operator is trying to diagnose.
func TestProbe_ShouldReportNoMatchAsAnAnswer(t *testing.T) {
	t.Parallel()

	svc, dir, _ := newProbeService(t)
	dir.entries = nil
	dir.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
		return &ldap.SearchResult{}, nil
	}

	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "nobody"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v, want a successful probe reporting no match", err)
	}
	if got.Matched != 0 {
		t.Errorf("matched = %d, want 0", got.Matched)
	}
	if got.Entry != nil {
		t.Error("no entry matched, so none should be reported")
	}
}

// TestProbe_ShouldReportMoreThanOneMatch surfaces a filter that is too
// loose. The login path refuses anything but exactly one, so the count is
// the diagnosis.
func TestProbe_ShouldReportMoreThanOneMatch(t *testing.T) {
	t.Parallel()

	svc, dir, _ := newProbeService(t)
	dir.entries = []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{"uid": {"alice"}}),
		entry("uid=alice2,ou=people,dc=test", map[string][]string{"uid": {"alice2"}}),
	}

	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got.Matched != 2 {
		t.Errorf("matched = %d, want 2 so the operator can see the filter is too loose", got.Matched)
	}
}

// TestProbe_ShouldNeverWriteAnything is the guarantee the console prints.
// The probe reaches no persist call by any input.
func TestProbe_ShouldNeverWriteAnything(t *testing.T) {
	t.Parallel()

	svc, _, db := newProbeService(t)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	if _, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	var ldapRows int64
	if err := db.Model(&model.UserLDAP{}).Count(&ldapRows).Error; err != nil {
		t.Fatalf("count user_ldap: %v", err)
	}
	if ldapRows != 0 {
		t.Errorf("the probe wrote %d user_ldap rows, want none", ldapRows)
	}

	var groupRows int64
	if err := db.Model(&model.UserGroup{}).Count(&groupRows).Error; err != nil {
		t.Fatalf("count user_groups: %v", err)
	}
	if groupRows != 0 {
		t.Errorf("the probe wrote %d group rows, want none", groupRows)
	}

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt != nil {
		t.Error("the probe disabled a user")
	}
}

// TestProbe_ShouldReportAnUnverifiedConnection keeps a probe that only
// succeeded because certificate verification was off from reading like one
// that verified.
func TestProbe_ShouldReportAnUnverifiedConnection(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{probeTestEntry()}}
	cfg := ldapTestConfig(probeTestFields())
	cfg.LDAP.TLSInsecureSkipVerify = true
	svc, _ := newLDAPTestService(t, cfg, dir)

	got, err := svc.Probe(context.Background(), ProbeRequest{
		Mode:     ProbeFilterTemplate,
		Bindings: ProbeBindings{Username: "alice"},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !got.TLSInsecureSkipVerify {
		t.Error("the probe ran without verifying the directory certificate and did not say so")
	}
}

// TestProbe_ShouldFailWhenItCannotRunAtAll covers the cases that are not an
// answer about the directory: no service, an unreachable server, an
// unparseable filter, and a literal probe with nothing to send.
func TestProbe_ShouldFailWhenItCannotRunAtAll(t *testing.T) {
	t.Parallel()

	t.Run("should refuse when directory enrichment is disabled", func(t *testing.T) {
		t.Parallel()

		var svc *LDAPService
		if _, err := svc.Probe(context.Background(), ProbeRequest{Mode: ProbeFilterTemplate}); err == nil {
			t.Error("Probe() on a disabled service returned no error")
		}
	})

	t.Run("should report an unreachable directory", func(t *testing.T) {
		t.Parallel()

		dir := &fakeDirectory{dialErr: errors.New("connection refused")}
		svc, _ := newLDAPTestService(t, ldapTestConfig(probeTestFields()), dir)

		_, err := svc.Probe(context.Background(), ProbeRequest{
			Mode:     ProbeFilterTemplate,
			Bindings: ProbeBindings{Username: "alice"},
		})
		if err == nil {
			t.Error("Probe() against an unreachable directory returned no error")
		}
	})

	t.Run("should reject an unparseable template", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := newProbeService(t)
		_, err := svc.Probe(context.Background(), ProbeRequest{
			Mode:   ProbeFilterTemplate,
			Filter: "(uid={{.Username)",
		})
		if err == nil {
			t.Error("Probe() with an unparseable filter template returned no error")
		}
	})

	t.Run("should reject a literal probe with no filter", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := newProbeService(t)
		_, err := svc.Probe(context.Background(), ProbeRequest{Mode: ProbeFilterLiteral})
		if err == nil {
			t.Error("Probe() in literal mode with no filter returned no error")
		}
	})
}

// TestCapProbeValues_ShouldBoundWhatIsRendered covers the probe's own caps,
// which exist because asking for every attribute is the point of the
// feature.
func TestCapProbeValues_ShouldBoundWhatIsRendered(t *testing.T) {
	t.Parallel()

	many := make([]string, probeMaxValuesPerAttribute+7)
	for i := range many {
		many[i] = "value"
	}

	capped, dropped := capProbeValues(many)
	if len(capped) != probeMaxValuesPerAttribute {
		t.Errorf("kept %d values, want %d", len(capped), probeMaxValuesPerAttribute)
	}
	if dropped != 7 {
		t.Errorf("reported %d dropped values, want 7", dropped)
	}

	long, _ := capProbeValues([]string{strings.Repeat("x", probeMaxValueBytes+1)})
	if !strings.Contains(long[0], "too long to display") {
		t.Errorf("an oversized value rendered as %q, want a length note", long[0])
	}
}

// TestSuggestedFieldName_ShouldReadAsAConfigKey covers the small conversion
// that makes a suggestion pasteable.
func TestSuggestedFieldName_ShouldReadAsAConfigKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attr string
		want string
	}{
		{attr: "employeeType", want: "employee_type"},
		{attr: "uid", want: "uid"},
		{attr: "departmentNumber", want: "department_number"},
		{attr: "custom-attr", want: "custom_attr"},
		{attr: "SNname", want: "s_nname"},
	}

	for _, tt := range tests {
		t.Run(tt.attr, func(t *testing.T) {
			t.Parallel()

			if got := suggestedFieldName(tt.attr); got != tt.want {
				t.Errorf("suggestedFieldName(%q) = %q, want %q", tt.attr, got, tt.want)
			}
		})
	}
}
