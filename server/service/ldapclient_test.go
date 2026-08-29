package service

// Test methodology: table-driven unit tests. The filter templating is the
// security-critical half of this file — an unescaped value is filter
// injection — so it is tested against the values that would exploit it,
// not only against well-formed ones.

import (
	"strings"
	"testing"
)

// The escaping is injected around every action rather than offered as a
// function, so an operator cannot write an unescaped filter even by
// accident. These are the values that make that matter.
func TestFilterTemplate_ShouldEscapeEveryInterpolatedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     filterData
		want     string
	}{
		{
			name:     "a plain username interpolates unchanged",
			template: "(&(objectClass=person)(uid={{.Username}}))",
			data:     filterData{Username: "alice"},
			want:     "(&(objectClass=person)(uid=alice))",
		},
		{
			name:     "a wildcard cannot broaden the filter",
			template: "(uid={{.Username}})",
			data:     filterData{Username: "*"},
			want:     `(uid=\2a)`,
		},
		{
			name:     "parentheses cannot close the filter early",
			template: "(uid={{.Username}})",
			data:     filterData{Username: "a)(objectClass=*"},
			want:     `(uid=a\29\28objectClass=\2a)`,
		},
		{
			name:     "a backslash cannot start its own escape",
			template: "(uid={{.Username}})",
			data:     filterData{Username: `a\2a`},
			want:     `(uid=a\5c2a)`,
		},
		{
			name:     "a NUL byte is escaped",
			template: "(uid={{.Username}})",
			data:     filterData{Username: "a\x00b"},
			want:     `(uid=a\00b)`,
		},
		{
			name:     "the DN is escaped like any other value",
			template: "(owner={{.DN}})",
			data:     filterData{DN: "cn=alice,ou=people"},
			want:     `(owner=cn=alice,ou=people)`,
		},
		{
			name:     "an extra claim is escaped",
			template: "(employeeNumber={{.Extra.empno}})",
			data:     filterData{Extra: map[string]string{"empno": "12)34"}},
			want:     `(employeeNumber=12\2934)`,
		},
		{
			name:     "a primary-entry attribute is escaped",
			template: "(authorizedUser={{.Attr.employeeNumber}})",
			data:     filterData{Attr: map[string]string{"employeeNumber": "*"}},
			want:     `(authorizedUser=\2a)`,
		},
		{
			name:     "index syntax reaches an awkward attribute name",
			template: `(authorizedUser={{index .Attr "employee-number"}})`,
			data:     filterData{Attr: map[string]string{"employee-number": "E-1"}},
			want:     `(authorizedUser=E-1)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := parseFilterTemplate("test", tt.template)
			if err != nil {
				t.Fatalf("parseFilterTemplate() error = %v", err)
			}
			got, err := tmpl.execute(tt.data)
			if err != nil {
				t.Fatalf("execute() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("execute() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Double-escaping would corrupt a filter as surely as not escaping would
// expose one, so an author who wrote the call explicitly gets it once.
func TestFilterTemplate_ShouldNotDoubleEscapeAnExplicitCall(t *testing.T) {
	t.Parallel()

	tmpl, err := parseFilterTemplate("test", "(uid={{ldapEscape .Username}})")
	if err != nil {
		t.Fatalf("parseFilterTemplate() error = %v", err)
	}
	got, err := tmpl.execute(filterData{Username: "*"})
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if got != `(uid=\2a)` {
		t.Errorf("execute() = %q, want a single escaping", got)
	}
}

// A filter that rendered to nothing would match far too much, so it is
// refused rather than sent to the directory.
func TestFilterTemplate_ShouldRefuseAnEmptyRender(t *testing.T) {
	t.Parallel()

	tmpl, err := parseFilterTemplate("test", "{{.Username}}")
	if err != nil {
		t.Fatalf("parseFilterTemplate() error = %v", err)
	}
	if _, err := tmpl.execute(filterData{Username: ""}); err == nil {
		t.Error("execute() error = nil, want a refusal for an empty filter")
	}
}

func TestParseFilterTemplate_ShouldRejectAMalformedTemplate(t *testing.T) {
	t.Parallel()

	if _, err := parseFilterTemplate("test", "(uid={{.Username)"); err == nil {
		t.Error("parseFilterTemplate() error = nil, want a parse failure")
	}
}

// Attributes a search filter reads off the primary entry are requested
// automatically, so nothing has to be duplicated as an extra field.
func TestReferencedAttrNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		want     []string
	}{
		{name: "no references", template: "(uid={{.Username}})", want: nil},
		{name: "dotted", template: "(authorizedUser={{.Attr.employeeNumber}})", want: []string{"employeeNumber"}},
		{name: "index form", template: `(x={{index .Attr "employee-number"}})`, want: []string{"employee-number"}},
		{
			name:     "several, deduped",
			template: "(&(a={{.Attr.one}})(b={{.Attr.two}})(c={{.Attr.one}}))",
			want:     []string{"one", "two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := referencedAttrNames(tt.template)
			if len(got) != len(tt.want) {
				t.Fatalf("referencedAttrNames() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("referencedAttrNames() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// memberOf yields DNs; the allowlist compares names. The reduced name is
// what has to match, and a value that is already a name must survive
// unchanged.
func TestReduceGroupName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "a DN reduces to its CN", value: "cn=soc,ou=groups,dc=example,dc=net", want: "soc"},
		{name: "a DN with spaces in the CN", value: "CN=SSH Users,OU=Groups,DC=example,DC=net", want: "SSH Users"},
		{name: "a bare name is kept", value: "soc", want: "soc"},
		{name: "a name with spaces is kept", value: "SSH Users", want: "SSH Users"},
		{name: "an empty value is kept", value: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := reduceGroupName(tt.value); got != tt.want {
				t.Errorf("reduceGroupName(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestWrapFilterActions_ShouldLeaveNonValueActionsAlone(t *testing.T) {
	t.Parallel()

	// A comment carries no value to escape, and wrapping it would be a
	// parse error rather than a security improvement.
	got := wrapFilterActions("(uid=x){{/* a note */}}")
	if strings.Contains(got, "ldapEscape (/*") {
		t.Errorf("wrapFilterActions() wrapped a comment: %q", got)
	}
}
