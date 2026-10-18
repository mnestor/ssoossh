package service

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/config"
)

// newNSSParser builds a service for parsing only: no database is touched.
func newNSSParser(t *testing.T, mutate func(*config.Config)) *NSSDirectoryService {
	t.Helper()
	cfg := nssTestConfig()
	if mutate != nil {
		mutate(cfg)
	}
	svc, err := NewNSSDirectoryService(cfg, nil)
	if err != nil {
		t.Fatalf("NewNSSDirectoryService() error = %v", err)
	}
	return svc
}

// strp returns a pointer to s.
func strp(s string) *string { return &s }

func TestNSSParseAccount_ShouldReadTheMappedAttributesWhenTheEntryIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.Config)
		entry  *ldap.Entry
		want   nssAccount
	}{
		{
			name:  "should read every field when the defaults map the entry",
			entry: nssAccountEntry("", "ada.lovelace", 10001, 20001, map[string][]string{"displayName": {"Ada Lovelace"}}),
			want: nssAccount{
				DN: "uid=ada.lovelace,ou=people,dc=test", Name: "ada.lovelace", UID: 10001, GID: 20001,
				Home: strp("/home/ada.lovelace"), Shell: strp("/bin/bash"), Gecos: "Ada Lovelace",
			},
		},
		{
			name: "should read every field from its own attribute when every key is remapped",
			mutate: func(c *config.Config) {
				c.NSS.Attributes = config.NSSAttributes{
					Name: "sAMAccountName", UID: "employeeUid", GID: "employeeGid", Home: "unixHome", Shell: "unixShell",
					GroupName: "groupLabel", GroupGID: "groupNumber", GroupMember: "member",
					GecosAttributes: []string{"fullName"},
				}
			},
			entry: entry("cn=x,dc=test", map[string][]string{
				"sAMAccountName": {"bob"}, "employeeUid": {"4242"}, "employeeGid": {"4343"},
				"unixHome": {"/srv/bob"}, "unixShell": {"/bin/zsh"}, "fullName": {"Bob B"},
				"uid": {"wrong"}, "uidNumber": {"1"}, "homeDirectory": {"/wrong"},
			}),
			want: nssAccount{DN: "cn=x,dc=test", Name: "bob", UID: 4242, GID: 4343, Home: strp("/srv/bob"), Shell: strp("/bin/zsh"), Gecos: "Bob B"},
		},
		{
			name:  "should leave home and shell nil when the entry has neither",
			entry: nssAccountEntry("", "svc-backup", 900, 900, map[string][]string{"homeDirectory": nil, "loginShell": nil}),
			want:  nssAccount{DN: "uid=svc-backup,ou=people,dc=test", Name: "svc-backup", UID: 900, GID: 900, Gecos: "svc-backup"},
		},
		{
			name:  "should match attribute names without regard to case when the directory changes it",
			entry: entry("uid=c,dc=test", map[string][]string{"UID": {"carol"}, "uidnumber": {"7"}, "GIDNUMBER": {"8"}}),
			want:  nssAccount{DN: "uid=c,dc=test", Name: "carol", UID: 7, GID: 8, Gecos: "carol"},
		},
		{
			name:  "should accept the top of the uint32 range when uid and gid are at the boundary",
			entry: nssAccountEntry("", "max", 4294967295, 0, nil),
			want: nssAccount{
				DN: "uid=max,ou=people,dc=test", Name: "max", UID: 4294967295, GID: 0,
				Home: strp("/home/max"), Shell: strp("/bin/bash"), Gecos: "max",
			},
		},
		{
			name:   "should capture the directory id when ldap.id_attribute is configured",
			mutate: func(c *config.Config) { c.LDAP.IDAttribute = "entryUUID" },
			entry:  nssAccountEntry("", "dora", 11, 11, map[string][]string{"entryUUID": {"6f1c-entry"}, "homeDirectory": nil, "loginShell": nil}),
			want:   nssAccount{DN: "uid=dora,ou=people,dc=test", DirectoryID: "6f1c-entry", Name: "dora", UID: 11, GID: 11, Gecos: "dora"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newNSSParser(t, tt.mutate)
			got, err := svc.parseAccount(tt.entry)
			if err != nil {
				t.Fatalf("parseAccount() error = %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("parseAccount() =\n %+v\nwant\n %+v", *got, tt.want)
			}
		})
	}
}

func TestNSSParseAccount_ShouldRejectTheEntryWhenAValueDoesNotParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   *ldap.Entry
		wantErr string
	}{
		{name: "should reject the entry when it has no uid attribute", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uidNumber": nil}), wantErr: "no uidNumber"},
		{name: "should reject the entry when the uid is not a number", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uidNumber": {"12a"}}), wantErr: "uidNumber"},
		{name: "should reject the entry when the uid is negative", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uidNumber": {"-1"}}), wantErr: "uidNumber"},
		{name: "should reject the entry when the uid is past uint32", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uidNumber": {"4294967296"}}), wantErr: "uidNumber"},
		{name: "should reject the entry when the uid has a space", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uidNumber": {" 5"}}), wantErr: "uidNumber"},
		{name: "should reject the entry when it has no gid attribute", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"gidNumber": nil}), wantErr: "no gidNumber"},
		{name: "should reject the entry when it has no name attribute", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"uid": nil}), wantErr: "no uid attribute"},
		{name: "should reject the entry when the name is all digits", entry: nssAccountEntry("", "1234", 1, 1, nil), wantErr: "all digits"},
		{name: "should reject the entry when the name has a colon", entry: nssAccountEntry("uid=x,dc=test", "a:b", 1, 1, nil), wantErr: "':'"},
		{name: "should reject the entry when the name is longer than 32 bytes", entry: nssAccountEntry("", strings.Repeat("n", 33), 1, 1, nil), wantErr: "33 bytes"},
		{name: "should reject the entry when the home is relative", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"homeDirectory": {"home/a"}}), wantErr: "not an absolute path"},
		{name: "should reject the entry when the home has a colon", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"homeDirectory": {"/home/a:b"}}), wantErr: "control character or ':'"},
		{name: "should reject the entry when the home has a newline", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"homeDirectory": {"/home/a\nroot"}}), wantErr: "control character"},
		{name: "should reject the entry when the home is not UTF-8", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"homeDirectory": {"/home/\xff"}}), wantErr: "UTF-8"},
		{name: "should reject the entry when the shell is relative", entry: nssAccountEntry("", "a", 1, 1, map[string][]string{"loginShell": {"bash"}}), wantErr: "loginShell"},
	}

	svc := newNSSParser(t, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.parseAccount(tt.entry)
			if !errors.Is(err, errNSSEntry) {
				t.Fatalf("parseAccount() error = %v, want one wrapping errNSSEntry", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseAccount() error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestNSSParseGroup_ShouldReadOrRejectTheEntryWhenParsed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   *ldap.Entry
		want    *nssGroup
		wantErr string
	}{
		{
			name:  "should read the name, gid and members when the entry is valid",
			entry: nssGroupEntry("devs.team", 5000, "ada", "bob"),
			want:  &nssGroup{DN: "cn=devs.team,ou=groups,dc=test", Name: "devs.team", GID: 5000, Members: []string{"ada", "bob"}},
		},
		{
			name:  "should read no members when the group lists none",
			entry: nssGroupEntry("empty", 5001),
			want:  &nssGroup{DN: "cn=empty,ou=groups,dc=test", Name: "empty", GID: 5001, Members: []string{}},
		},
		{name: "should reject the group when the gid is not a number", entry: entry("cn=x,dc=test", map[string][]string{"cn": {"x"}, "gidNumber": {"x"}}), wantErr: "gidNumber"},
		{name: "should reject the group when the name fails the POSIX rules", entry: entry("cn=x,dc=test", map[string][]string{"cn": {"a/b"}, "gidNumber": {"1"}}), wantErr: "'/'"},
	}

	svc := newNSSParser(t, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := svc.parseGroup(tt.entry)
			if tt.wantErr != "" {
				if !errors.Is(err, errNSSEntry) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseGroup() error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGroup() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseGroup() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNSSGecos_ShouldRenderAndCleanTheFieldWhenAnAccountIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		template    string
		attrs       map[string][]string
		want        string
		wantCleaned bool
	}{
		{name: "should use the first gecos attribute when it is set", attrs: map[string][]string{"displayName": {"Ada Lovelace"}, "cn": {"ada"}}, want: "Ada Lovelace"},
		{name: "should fall back to the next attribute when the first is empty", attrs: map[string][]string{"displayName": {""}, "cn": {"Ada L"}}, want: "Ada L"},
		{name: "should fall back to the name when no gecos attribute is set", attrs: map[string][]string{"cn": nil}, want: "ada"},
		{name: "should replace a colon with a space when the value has one", attrs: map[string][]string{"displayName": {"Ada:Lovelace"}}, want: "Ada Lovelace", wantCleaned: true},
		{name: "should replace a newline with a space when the value has one", attrs: map[string][]string{"displayName": {"Ada\nroot::0:0"}}, want: "Ada root 0 0", wantCleaned: true},
		{name: "should replace a bare control byte when the value has one", attrs: map[string][]string{"displayName": {"Ada\x07Lovelace"}}, want: "Ada Lovelace", wantCleaned: true},
		{name: "should use the name when the value is not UTF-8", attrs: map[string][]string{"displayName": {"Ada \xff"}}, want: "ada", wantCleaned: true},
		{name: "should cut the value at 256 bytes when it is longer", attrs: map[string][]string{"displayName": {strings.Repeat("x", 300)}}, want: strings.Repeat("x", 256), wantCleaned: true},
		{name: "should use the name when the template renders empty", template: "{{.Attr.cn}}", attrs: map[string][]string{"cn": nil}, want: "ada", wantCleaned: true},
		{name: "should join every value when the template uses join", template: `{{join .Attr.cn ", "}}`, attrs: map[string][]string{"cn": {"Ada", "Countess"}}, want: "Ada, Countess"},
		{name: "should render the first value when a multi-valued attribute is interpolated", template: "{{.Attr.cn}}", attrs: map[string][]string{"cn": {"Ada", "Countess"}}, want: "Ada"},
		{name: "should render nothing when the attribute is outside the allow list", template: "{{.Name}}[{{.Attr.userPassword}}]", attrs: map[string][]string{"userPassword": {"secret"}}, want: "ada[]"},
		{name: "should render nothing rather than MISSING when an allowed attribute is absent", template: "{{.Name}}[{{.Attr.displayName}}]", attrs: map[string][]string{"displayName": nil}, want: "ada[]"},
		{name: "should treat a directive in a value as text when the value holds one", attrs: map[string][]string{"displayName": {"{{.Name}}"}}, want: "{{.Name}}"},
		{name: "should join a plain field when the template joins the name", template: `{{join .Name "-"}}`, want: "ada"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newNSSParser(t, func(c *config.Config) {
				if tt.template != "" {
					c.NSS.GecosTemplate = tt.template
				}
			})
			got, cleaned := svc.renderGecos("ada", nssAccountEntry("", "ada", 1, 1, tt.attrs))
			if got != tt.want {
				t.Errorf("renderGecos() = %q, want %q", got, tt.want)
			}
			if cleaned != tt.wantCleaned {
				t.Errorf("renderGecos() cleaned = %v, want %v", cleaned, tt.wantCleaned)
			}
		})
	}
}

func TestNSSGecosJoin_ShouldRenderEachValueKindWhenJoined(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "should join an attribute's values when given one", value: gecosValue{values: []string{"a", "b"}}, want: "a+b"},
		{name: "should join a slice when given one", value: []string{"x", "y"}, want: "x+y"},
		{name: "should render nothing when given nil", value: nil, want: ""},
		{name: "should print a scalar when given a plain field", value: "name", want: "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := gecosJoin(tt.value, "+"); got != tt.want {
				t.Errorf("gecosJoin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewNSSDirectoryService_ShouldFailStartupWhenAFilterOrTheTemplateIsBad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{name: "should fail when user_filter does not parse", mutate: func(c *config.Config) { c.NSS.UserFilter = "(objectClass=posixAccount" }, wantErr: "nss.user_filter"},
		{name: "should fail when group_filter is not parenthesised", mutate: func(c *config.Config) { c.NSS.GroupFilter = "objectClass=posixGroup" }, wantErr: "nss.group_filter"},
		{name: "should fail when disabled_filter does not parse", mutate: func(c *config.Config) { c.NSS.DisabledFilter = "(&(nsAccountLock=TRUE)" }, wantErr: "nss.disabled_filter"},
		{name: "should fail when the gecos template does not parse", mutate: func(c *config.Config) { c.NSS.GecosTemplate = "{{.Name" }, wantErr: "nss.gecos_template"},
		{name: "should fail when the gecos template names a missing field", mutate: func(c *config.Config) { c.NSS.GecosTemplate = "{{.Nmae}}" }, wantErr: "nss.gecos_template"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			tt.mutate(cfg)
			if _, err := NewNSSDirectoryService(cfg, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewNSSDirectoryService() error = %v, want one naming %s", err, tt.wantErr)
			}
		})
	}
}

func TestNewNSSDirectoryService_ShouldReturnNilWhenNSSIsDisabled(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.NSS.Enabled = false
	svc, err := NewNSSDirectoryService(cfg, nil)
	if err != nil || svc != nil {
		t.Errorf("NewNSSDirectoryService() = %v, %v; want nil, nil", svc, err)
	}
}

func TestNSSMemberValues_ShouldCompareDNsAndNamesWhenMembersAreResolved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "should lower-case types and values when a DN is normalised", got: normalizeDN("UID=Ada,OU=People,DC=Test"), want: "uid=ada,ou=people,dc=test"},
		{name: "should drop spacing when separators are spaced", got: normalizeDN("uid=ada, ou=people,  dc=test"), want: "uid=ada,ou=people,dc=test"},
		{name: "should sort the parts when an RDN is multi-valued", got: normalizeDN("uid=a+cn=b,dc=test"), want: "cn=b+uid=a,dc=test"},
		{name: "should lower-case and trim when the value is not a DN", got: normalizeDN("  Not A DN  "), want: "not a dn"},
		{name: "should strip the unique identifier when a uniqueMember carries one", got: memberDN("uid=ada,dc=test#'0101'B"), want: "uid=ada,dc=test"},
		{name: "should keep the value when it has no unique identifier", got: memberDN("uid=ada,dc=test"), want: "uid=ada,dc=test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestNSSMembersAreDNs_ShouldDecideByTheAttributeNameWhenAsked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attr string
		want bool
	}{
		{"member", true}, {"Member", true}, {"uniqueMember", true}, {"uniquemember", true},
		{"memberUid", false}, {"members", false}, {"", false},
	}
	for _, tt := range tests {
		t.Run("should report "+map[bool]string{true: "DNs", false: "names"}[tt.want]+" when the attribute is "+tt.attr, func(t *testing.T) {
			t.Parallel()

			if got := nssMembersAreDNs(tt.attr); got != tt.want {
				t.Errorf("nssMembersAreDNs(%q) = %v, want %v", tt.attr, got, tt.want)
			}
		})
	}
}

func TestNSSParseGroup_ShouldReadEachFieldFromItsOwnAttributeWhenEveryKeyIsRemapped(t *testing.T) {
	t.Parallel()

	svc := newNSSParser(t, func(c *config.Config) {
		c.NSS.Attributes.GroupName = "groupLabel"
		c.NSS.Attributes.GroupGID = "groupNumber"
		c.NSS.Attributes.GroupMember = "uniqueMember"
		c.LDAP.IDAttribute = "entryUUID"
	})
	got, err := svc.parseGroup(nssEntry("cn=x,dc=test", map[string][]string{
		"groupLabel": {"ops"}, "groupNumber": {"700"}, "uniqueMember": {"uid=a,dc=test"},
		"cn": {"wrong"}, "gidNumber": {"1"}, "memberUid": {"wrong"}, "entryUUID": {"g-1"},
	}))
	if err != nil {
		t.Fatalf("parseGroup() error = %v", err)
	}
	want := &nssGroup{DN: "cn=x,dc=test", DirectoryID: "g-1", Name: "ops", GID: 700, Members: []string{"uid=a,dc=test"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseGroup() = %+v, want %+v", got, want)
	}
}
