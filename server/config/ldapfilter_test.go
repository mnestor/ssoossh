package config

// Test methodology: table-driven unit tests over the user_filter identity
// check, first as the parse-tree walk on its own, then through
// LDAPConfig.Validate and NewConfig, which is the path startup and
// `ssoosshd config check` take. No directory is involved: the check is
// static by design.

import (
	"strings"
	"testing"
)

// should accept a filter that reads an identity field in any form the
// template language allows, and refuse one that reads none.
func TestValidateLDAPUserFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		filter  string
		wantErr string
	}{
		{name: "should accept a filter when it references the username", filter: "(uid={{.Username}})"},
		{name: "should accept a filter when it references the email", filter: "(mail={{.Email}})"},
		{name: "should accept a filter when it references the subject", filter: "(entryUUID={{.Subject}})"},
		{name: "should accept a filter when it references an extra claim", filter: "(employeeNumber={{.Extra.employee_id}})"},
		{name: "should accept a filter when it indexes the extra claims", filter: `(employeeNumber={{index .Extra "employee-id"}})`},
		{name: "should accept a filter when it reads the root through $", filter: "(uid={{$.Username}})"},
		{name: "should accept a filter when the field feeds a pipeline", filter: `(uid={{ .Username | printf "%s" }})`},
		{name: "should accept a filter when the field is a function argument", filter: `(uid={{printf "%s-adm" .Username}})`},
		{name: "should accept a filter when the field is already escaped by hand", filter: "(uid={{ldapEscape .Username}})"},
		{name: "should accept a filter when the field sits in a conditional", filter: "{{if .Email}}(mail={{.Email}}){{end}}"},
		{name: "should accept a filter when the field is in a called template", filter: `{{define "id"}}{{.Username}}{{end}}(uid={{template "id" .}})`},
		{name: "should accept a filter when the field is in a parenthesized pipeline", filter: "(uid={{ldapEscape (.Username)}})"},
		{name: "should accept a filter when the field is bound to a variable", filter: "{{$u := .Username}}(uid={{$u}})"},
		{name: "should accept a filter when the field drives a with block", filter: "{{with .Extra}}(employeeNumber={{.employee_id}}){{end}}"},
		{name: "should accept a filter when the field drives a range", filter: "{{range $k, $v := .Extra}}({{$k}}={{$v}}){{end}}"},
		{name: "should accept a filter when a field is read off a chain", filter: "(employeeNumber={{(.Extra).employee_id}})"},
		{name: "should accept a filter when the field is only in an else branch", filter: "{{if false}}(objectClass=person){{else}}(uid={{.Username}}){{end}}"},
		{name: "should accept the lldap example when it pins the object class", filter: "(&(objectClass=person)(uid={{.Username}}))"},
		{
			// The static check cannot see that this matches everyone; the
			// probe reports it and login fails closed on several entries.
			name:   "should accept an or-filter when it references the username",
			filter: "(|(objectClass=person)(uid={{.Username}}))",
		},
		{name: "should refuse a filter when it references nothing", filter: "(objectClass=person)", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when the only field is in a comment", filter: "(objectClass=person){{/* .Username */}}", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when the only field is in a string literal", filter: `(uid={{".Username"}})`, wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when it reads only the primary entry DN", filter: "(manager={{.DN}})", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when it reads only primary entry attributes", filter: "(uid={{.Attr.uid}})", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when a field is only in a template nobody calls", filter: `{{define "id"}}{{.Username}}{{end}}(objectClass=person)`, wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when a conditional without else reads nothing", filter: "{{if true}}(objectClass=person){{end}}", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when a called template reads nothing", filter: `{{define "oc"}}person{{end}}(objectClass={{template "oc"}})`, wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when it calls a template nobody defined", filter: `(uid={{template "nope" .}})`, wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when it reads a field off a bound variable only", filter: "{{$x := 1}}(uid={{$x}})", wantErr: errUserFilterNoIdentity},
		{name: "should refuse a filter when a lookalike field name is used", filter: "(uid={{.Usernames}})", wantErr: errUserFilterNoIdentity},
		{name: "should report a parse error when the template does not parse", filter: "(uid={{.Username}", wantErr: "not a valid template"},
		{name: "should report a parse error when the template names an unknown function", filter: "(uid={{.Username | nosuch}})", wantErr: "not a valid template"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateLDAPUserFilter(tt.filter)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateLDAPUserFilter(%q) error = %v, want nil", tt.filter, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateLDAPUserFilter(%q) error = %v, want one containing %q", tt.filter, err, tt.wantErr)
			}
		})
	}
}

// should terminate when a defined template calls itself, rather than walk
// it forever.
func TestTemplateReferencesFields_ShouldTerminateWhenATemplateRecurses(t *testing.T) {
	t.Parallel()

	got, err := templateReferencesFields(`{{define "loop"}}{{template "loop" .}}{{end}}{{template "loop" .}}`, ldapIdentityFields)
	if err != nil || got {
		t.Fatalf("templateReferencesFields() = %v, %v, want false, nil", got, err)
	}
}

// should report no reference when the template is plain text with no
// actions at all.
func TestTemplateReferencesFields_ShouldReportNothingWhenTheTemplateIsEmpty(t *testing.T) {
	t.Parallel()

	got, err := templateReferencesFields("", ldapIdentityFields)
	if err != nil || got {
		t.Fatalf("templateReferencesFields(\"\") = %v, %v, want false, nil", got, err)
	}
}

// should run the identity check through LDAPConfig.Validate only when LDAP
// is on, and keep the required-filter error for an unset filter.
func TestLDAPConfig_ShouldValidateTheUserFilterIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*LDAPConfig)
		wantErr string
	}{
		{name: "should accept a filter when it references the identity", mutate: func(*LDAPConfig) {}},
		{
			name:    "should refuse a filter when it references no identity field",
			mutate:  func(c *LDAPConfig) { c.UserFilter = "(objectClass=person)" },
			wantErr: errUserFilterNoIdentity,
		},
		{
			name:    "should keep the required error when the filter is unset",
			mutate:  func(c *LDAPConfig) { c.UserFilter = "" },
			wantErr: "ldap.user_filter is required",
		},
		{
			name: "should skip the check when LDAP is disabled",
			mutate: func(c *LDAPConfig) {
				c.Enabled = false
				c.UserFilter = "(objectClass=person)"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := validLDAP()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// should refuse to load a config file whose user_filter names nobody, and
// load the same file with each schema preset when the filter does name the
// identity: no preset sets ldap.user_filter, so none can trip the check.
func TestNewConfig_ShouldCheckTheUserFilterIdentity(t *testing.T) {
	t.Parallel()

	ldapBlock := func(schema, filter string) string {
		out := "ssh_key: \"test-key-material\"\nldap:\n  enabled: true\n  url: ldaps://ldap.example.com\n  base_dn: dc=example,dc=com\n"
		if schema != "" {
			out += "  schema: " + schema + "\n"
		}
		return out + "  user_filter: \"" + filter + "\"\n"
	}

	type testCase struct {
		name    string
		file    string
		wantErr string
	}
	tests := []testCase{
		{
			name:    "should refuse to start when the user filter references no identity field",
			file:    ldapBlock("", "(objectClass=person)"),
			wantErr: errUserFilterNoIdentity,
		},
		{
			name: "should start when the user filter references the username",
			file: ldapBlock("", "(&(objectClass=person)(uid={{.Username}}))"),
		},
	}
	for _, schema := range LDAPSchemas() {
		tests = append(tests, testCase{
			name: "should start with the " + schema + " preset when the user filter references the username",
			file: ldapBlock(schema, "(&(objectClass=person)(uid={{.Username}}))"),
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadNSSConfig(t, tt.file)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewConfig() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewConfig() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
