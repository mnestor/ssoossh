package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/service"
)

// TestParseExtraBindings_ShouldReadNameValuePairs covers the repeatable
// --extra flag, which is what lets a filter referencing {{.Extra.x}} be
// tested at all.
func TestParseExtraBindings_ShouldReadNameValuePairs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pairs   []string
		want    map[string]string
		wantErr bool
	}{
		{name: "no flags yields no bindings", pairs: nil},
		{
			name:  "one pair",
			pairs: []string{"dept=platform"},
			want:  map[string]string{"dept": "platform"},
		},
		{
			name:  "several pairs",
			pairs: []string{"dept=platform", "site=lon"},
			want:  map[string]string{"dept": "platform", "site": "lon"},
		},
		{
			name:  "a value containing an equals sign keeps it",
			pairs: []string{"dn=uid=alice,ou=People"},
			want:  map[string]string{"dn": "uid=alice,ou=People"},
		},
		{
			name:  "an empty value is a value",
			pairs: []string{"dept="},
			want:  map[string]string{"dept": ""},
		},
		{name: "no equals sign is an error", pairs: []string{"dept"}, wantErr: true},
		{name: "an empty name is an error", pairs: []string{"=platform"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseExtraBindings(tt.pairs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseExtraBindings(%v) returned no error", tt.pairs)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExtraBindings(%v) error = %v", tt.pairs, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for name, value := range tt.want {
				if got[name] != value {
					t.Errorf("binding %q = %q, want %q", name, got[name], value)
				}
			}
		})
	}
}

// TestWriteProbeResult_ShouldReportTheThreeStages covers the report an
// operator reads: the entry, the mapping, then the merge.
func TestWriteProbeResult_ShouldReportTheThreeStages(t *testing.T) {
	t.Parallel()

	result := &service.ProbeResult{
		BaseDN:     "dc=example,dc=net",
		FilterSent: "(&(objectClass=person)(uid=alice))",
		Mode:       service.ProbeFilterTemplate,
		Attributes: []string{"*"},
		Matched:    1,
		Elapsed:    34 * time.Millisecond,
		Timeout:    5 * time.Second,
		Entry: &service.ProbeEntry{
			DN: "uid=alice,ou=People,dc=example,dc=net",
			Attributes: []service.ProbeAttribute{
				{Name: "memberOf", Values: []string{"cn=platform,dc=example,dc=net"}, Configured: true},
				{Name: "departmentNumber", Values: []string{"4120"}},
			},
		},
		Fields: []service.ProbeField{{
			Name:             "groups",
			Attribute:        "memberOf",
			AttributePresent: true,
			AttributeValues:  []string{"cn=platform,dc=example,dc=net"},
			Values:           []string{"cn=platform,dc=example,dc=net"},
		}},
		Merge: []service.ProbeMerge{{
			Name:    "groups",
			Action:  "persist-groups",
			Kept:    []string{"platform"},
			Dropped: []string{"vpn-legacy"},
		}},
		Suggestions: []service.ProbeSuggestion{{
			Reason: "departmentNumber is on the entry but no configured field reads it",
			YAML:   "ldap:\n  fields:\n    department_number: departmentNumber",
		}},
	}

	var out bytes.Buffer
	if err := writeProbeResult(&out, result); err != nil {
		t.Fatalf("writeProbeResult() error = %v", err)
	}
	report := out.String()

	for _, want := range []string{
		"(&(objectClass=person)(uid=alice))",
		"RFC 4515 escaped",
		"uid=alice,ou=People,dc=example,dc=net",
		"departmentNumber",
		"field mapping",
		"merge and allowlist",
		"dropped: vpn-legacy",
		"Nothing was written.",
		"department_number: departmentNumber",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}

// TestWriteProbeResult_ShouldSayALiteralFilterWasNotEscaped keeps the two
// modes distinguishable in the output, since escaping is the only thing that
// differs between them.
func TestWriteProbeResult_ShouldSayALiteralFilterWasNotEscaped(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := writeProbeResult(&out, &service.ProbeResult{
		BaseDN:     "dc=example,dc=net",
		FilterSent: "(uid=alice)",
		Mode:       service.ProbeFilterLiteral,
		Attributes: []string{"*"},
	})
	if err != nil {
		t.Fatalf("writeProbeResult() error = %v", err)
	}
	if !strings.Contains(out.String(), "sent verbatim") {
		t.Errorf("a literal probe did not say the filter was sent as typed:\n%s", out.String())
	}
}

// TestWriteProbeResult_ShouldTreatNoMatchAsAnAnswer keeps "found nothing"
// from reading as "the probe failed": it is the most common thing being
// diagnosed.
func TestWriteProbeResult_ShouldTreatNoMatchAsAnAnswer(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := writeProbeResult(&out, &service.ProbeResult{
		BaseDN:     "dc=example,dc=net",
		FilterSent: "(uid=nobody)",
		Mode:       service.ProbeFilterTemplate,
		Attributes: []string{"*"},
		Matched:    0,
	})
	if err != nil {
		t.Fatalf("writeProbeResult() error = %v", err)
	}
	if !strings.Contains(out.String(), "what a login would see") {
		t.Errorf("a probe that matched nothing did not explain itself:\n%s", out.String())
	}
}

// TestWriteProbeResult_ShouldWarnAboutALooseFilter surfaces the case the
// login path refuses outright.
func TestWriteProbeResult_ShouldWarnAboutALooseFilter(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := writeProbeResult(&out, &service.ProbeResult{
		BaseDN:                "dc=example,dc=net",
		FilterSent:            "(objectClass=person)",
		Mode:                  service.ProbeFilterTemplate,
		Attributes:            []string{"*"},
		Matched:               3,
		TLSInsecureSkipVerify: true,
		Entry:                 &service.ProbeEntry{DN: "uid=alice,dc=example,dc=net"},
	})
	if err != nil {
		t.Fatalf("writeProbeResult() error = %v", err)
	}

	report := out.String()
	if !strings.Contains(report, "too loose") {
		t.Errorf("a filter matching three entries was not flagged:\n%s", report)
	}
	if !strings.Contains(report, "not verified") {
		t.Errorf("an unverified connection was not reported:\n%s", report)
	}
}

// TestOrDash_ShouldMakeAnEmptyFieldVisible keeps an empty resolution from
// rendering as a blank line that reads like a formatting bug.
func TestOrDash_ShouldMakeAnEmptyFieldVisible(t *testing.T) {
	t.Parallel()

	if got := orDash(nil); got != "—" {
		t.Errorf("orDash(nil) = %q, want a dash", got)
	}
	if got := orDash([]string{"a", "b"}); got != "a, b" {
		t.Errorf("orDash([a b]) = %q, want %q", got, "a, b")
	}
}
