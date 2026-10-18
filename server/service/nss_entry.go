package service

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	// text/template, not html/template: the gecos template renders a passwd
	// field for getent, never HTML, and HTML escaping would corrupt names
	// such as "O'Brien" instead of preventing anything.
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"text/template"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/internal/posixname"
	"github.com/mnestor/ssoossh/server/config"
)

// nssAccount is one account entry read from the directory and parsed per
// nss.attributes: everything a posix_users row is written from. An entry
// becomes one only whole; a value that does not parse rejects the entry
// rather than leaving a field empty.
type nssAccount struct {
	DN          string
	DirectoryID string
	Name        string
	UID         uint32
	// GID is nil when the entry has no usable gid attribute, which only
	// nss.auto_private_groups "true" accepts (see accountGID).
	GID *uint32
	// Home and Shell are nil when the entry has no such attribute.
	Home  *string
	Shell *string
	// Gecos is the rendered and sanitised nss.gecos_template, and
	// GecosCleaned whether the sanitiser had to change the render.
	Gecos        string
	GecosCleaned bool
}

// nssGroup is one group entry read from the directory and parsed per
// nss.attributes.
type nssGroup struct {
	DN          string
	DirectoryID string
	Name        string
	GID         uint32
	// Members are the raw values of attributes.group_member: account names,
	// or DNs when the attribute holds DNs (see nssMembersAreDNs). Empty when
	// the entry was read without them.
	Members []string
}

// errNSSEntry wraps every reason an entry is rejected, so a caller can tell
// a bad entry (skipped with a warning) from a failure to read one.
var errNSSEntry = errors.New("directory entry rejected")

// nssMembersAreDNs reports whether the member attribute holds DNs rather
// than account names. Decided by the attribute's name: member and
// uniqueMember are the DN-valued membership attributes of RFC 2307bis,
// groupOfNames and groupOfUniqueNames, and everything else (memberUid
// first among them) is read as names.
func nssMembersAreDNs(attr string) bool {
	return strings.EqualFold(attr, "member") || strings.EqualFold(attr, "uniqueMember")
}

// nssValues returns every value of attr on e. Attribute names are matched
// without regard to case, as LDAP compares them: a directory may return
// uidnumber for a request that asked for uidNumber.
func nssValues(e *ldap.Entry, attr string) []string {
	return e.GetEqualFoldAttributeValues(attr)
}

// nssFirst returns the first value of attr on e, and whether there was one.
func nssFirst(e *ldap.Entry, attr string) (string, bool) {
	values := nssValues(e, attr)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// nssNumber reads attr off e as an unsigned 32-bit number: a uid or gid.
// Exactly the decimal digits the directory's integer syntax allows; a sign,
// a space or anything past 4294967295 rejects the entry.
func nssNumber(e *ldap.Entry, attr string) (uint32, error) {
	raw, ok := nssFirst(e, attr)
	if !ok {
		return 0, fmt.Errorf("%w: no %s attribute", errNSSEntry, attr)
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q is not a number from 0 to 4294967295", errNSSEntry, attr, raw)
	}
	return uint32(n), nil
}

// nssPath reads an optional path attribute (home or shell). Absent is nil.
// Present, it must be an absolute path that can sit in a passwd field: valid
// UTF-8, no control character (a newline ends the record) and no ':' (the
// field separator).
func nssPath(e *ldap.Entry, attr string) (*string, error) {
	raw, ok := nssFirst(e, attr)
	if !ok || raw == "" {
		return nil, nil //nolint:nilnil // an absent optional attribute is no value and no error.
	}
	switch {
	case !strings.HasPrefix(raw, "/"):
		return nil, fmt.Errorf("%w: %s %q is not an absolute path", errNSSEntry, attr, raw)
	case !utf8.ValidString(raw):
		return nil, fmt.Errorf("%w: %s is not valid UTF-8", errNSSEntry, attr)
	case strings.ContainsFunc(raw, func(r rune) bool { return r < ' ' || r == 0x7f || r == ':' }):
		return nil, fmt.Errorf("%w: %s %q contains a control character or ':'", errNSSEntry, attr, raw)
	}
	return &raw, nil
}

// nssName reads a mapped name attribute and holds it to the POSIX name
// rules every host applies (internal/posixname), verbatim: nothing is
// folded or trimmed, so the name stored is the name served.
func nssName(e *ldap.Entry, attr string) (string, error) {
	name, ok := nssFirst(e, attr)
	if !ok {
		return "", fmt.Errorf("%w: no %s attribute", errNSSEntry, attr)
	}
	if err := posixname.Valid(name); err != nil {
		return "", fmt.Errorf("%w: %s %q: %w", errNSSEntry, attr, name, err)
	}
	return name, nil
}

// parseAccount turns an account entry into an nssAccount, or rejects it
// with an error wrapping errNSSEntry that says which attribute failed.
func (s *NSSDirectoryService) parseAccount(e *ldap.Entry) (*nssAccount, error) {
	attrs := s.config.NSS.Attributes
	name, err := nssName(e, attrs.Name)
	if err != nil {
		return nil, err
	}
	uid, err := nssNumber(e, attrs.UID)
	if err != nil {
		return nil, err
	}
	gid, err := s.accountGID(e)
	if err != nil {
		return nil, err
	}
	home, err := nssPath(e, attrs.Home)
	if err != nil {
		return nil, err
	}
	shell, err := nssPath(e, attrs.Shell)
	if err != nil {
		return nil, err
	}
	gecos, cleaned := s.renderGecos(name, e)
	return &nssAccount{
		DN:           e.DN,
		DirectoryID:  directoryIDValue(e, s.config.LDAP.IDAttribute),
		Name:         name,
		UID:          uid,
		GID:          gid,
		Home:         home,
		Shell:        shell,
		Gecos:        gecos,
		GecosCleaned: cleaned,
	}, nil
}

// accountGID reads an account's gid attribute the way sssd's LDAP provider
// does for nss.auto_private_groups (sssd 2.14.0-beta1-37-g493e37fe,
// src/providers/ldap/sdap_async_users.c sdap_save_user):
//
//   - "true" (an MPG domain): the gid is optional. sssd stores the uid as
//     the gid, and keeps a gid it can read only as
//     origPrimaryGroupGidNumber; one that is absent, zero or does not
//     parse is dropped ("Missing GID" / "Cannot retrieve GID, won't save
//     the origPrimaryGroupGidNumber attribute", sdap_user_set_mpg). Nil
//     here stands for that.
//   - "hybrid" and "false": the gid is required, and an entry without one
//     is refused (ERR_NO_POSIX, "Cannot retrieve GID"). The warning names
//     the setting that would accept it.
func (s *NSSDirectoryService) accountGID(e *ldap.Entry) (*uint32, error) {
	attr := s.config.NSS.Attributes.GID
	gid, err := nssNumber(e, attr)
	if s.config.NSS.AutoPrivateGroups == config.AutoPrivateGroupsOn {
		if err != nil {
			if _, present := nssFirst(e, attr); present {
				s.log.Warn("ignoring a directory account's gid attribute that does not parse; nss.auto_private_groups is true, so its primary group is its private group",
					"dn", e.DN, "error", err)
			}
			return nil, nil //nolint:nilnil // no gid is a valid account under "true".
		}
		if gid == 0 {
			// sdap_user_set_mpg reads a zero gid as "the entry had no GID
			// number" and saves no origPrimaryGroupGidNumber.
			return nil, nil //nolint:nilnil // as above.
		}
		return &gid, nil
	}
	if err != nil {
		if _, present := nssFirst(e, attr); !present {
			return nil, fmt.Errorf("%w: no %s attribute, which nss.auto_private_groups %q requires (\"true\" would give the account a private group with gid equal to its uid)",
				errNSSEntry, attr, s.config.NSS.AutoPrivateGroups)
		}
		return nil, err
	}
	return &gid, nil
}

// parseGroup turns a group entry into an nssGroup, or rejects it as
// parseAccount does.
func (s *NSSDirectoryService) parseGroup(e *ldap.Entry) (*nssGroup, error) {
	attrs := s.config.NSS.Attributes
	name, err := nssName(e, attrs.GroupName)
	if err != nil {
		return nil, err
	}
	gid, err := nssNumber(e, attrs.GroupGID)
	if err != nil {
		return nil, err
	}
	return &nssGroup{
		DN:          e.DN,
		DirectoryID: directoryIDValue(e, s.config.LDAP.IDAttribute),
		Name:        name,
		GID:         gid,
		Members:     nssValues(e, attrs.GroupMember),
	}, nil
}

// gecosValue is one attribute as the gecos template sees it. Interpolated
// directly it renders the first value; join renders them all. Its zero
// value, which is what an attribute outside gecos_attributes or absent
// from the entry resolves to under missingkey=zero, renders the empty
// string: the key ID templates' MISSING placeholder would end up in a field
// every local user reads.
type gecosValue struct {
	values []string
}

// String renders the first value, or nothing.
func (v gecosValue) String() string {
	if len(v.values) == 0 {
		return ""
	}
	return v.values[0]
}

// gecosData is everything nss.gecos_template can reach. The directory's
// values are data here, never template text, so a value cannot carry a
// directive.
type gecosData struct {
	// Name is the mapped account name.
	Name string
	// DisplayName is the first non-empty value of gecos_attributes, in
	// their order, else Name.
	DisplayName string
	// Attr holds exactly the attributes listed in gecos_attributes.
	Attr map[string]gecosValue
}

// gecosJoin is the template's join function: every value of an attribute
// with sep between them. It takes any value so that a template which joins
// a plain field ({{join .Name " "}}) renders that field rather than failing
// every account.
func gecosJoin(v any, sep string) string {
	switch value := v.(type) {
	case gecosValue:
		return strings.Join(value.values, sep)
	case []string:
		return strings.Join(value, sep)
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

// parseGecosTemplate parses nss.gecos_template and renders it once against
// zero data, so a template that names a field gecosData does not have fails
// startup rather than every account. Parse alone checks only the syntax.
func parseGecosTemplate(src string) (*template.Template, error) {
	tmpl, err := template.New("gecos_template").
		Option("missingkey=zero").
		Funcs(template.FuncMap{"join": gecosJoin}).
		Parse(src)
	if err != nil {
		return nil, fmt.Errorf("nss.gecos_template does not parse: %w", err)
	}
	if err := tmpl.Execute(&strings.Builder{}, gecosData{}); err != nil {
		return nil, fmt.Errorf("nss.gecos_template does not render: %w", err)
	}
	return tmpl, nil
}

// renderGecos renders the gecos field for an account named name from e,
// then cleans it with posixname.SanitizeGECOS, returning the value to store
// and whether cleaning changed the render. A render that fails at run time
// stores the name, as an empty render would, and logs why.
func (s *NSSDirectoryService) renderGecos(name string, e *ldap.Entry) (string, bool) {
	data := gecosData{Name: name, DisplayName: name, Attr: map[string]gecosValue{}}
	displaySet := false
	for _, attr := range s.config.NSS.Attributes.GecosAttributes {
		values := nssValues(e, attr)
		data.Attr[attr] = gecosValue{values: values}
		if !displaySet && len(values) > 0 && values[0] != "" {
			data.DisplayName = values[0]
			displaySet = true
		}
	}

	var out strings.Builder
	if err := s.gecos.Execute(&out, data); err != nil {
		s.log.Warn("the gecos template failed for a directory entry; storing the account name",
			"dn", e.DN, "error", err)
		out.Reset()
	}
	return posixname.SanitizeGECOS(out.String(), name)
}

// normalizeDN returns a comparable form of dn: attribute types and values
// lower-cased, spacing around separators dropped, and a multi-valued RDN's
// parts sorted, so the same DN written two ways compares equal. A value
// that does not parse as a DN is only lower-cased and trimmed.
func normalizeDN(dn string) string {
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(dn))
	}
	rdns := make([]string, 0, len(parsed.RDNs))
	for _, rdn := range parsed.RDNs {
		parts := make([]string, 0, len(rdn.Attributes))
		for _, a := range rdn.Attributes {
			parts = append(parts, strings.ToLower(a.Type)+"="+strings.ToLower(a.Value))
		}
		slices.Sort(parts)
		rdns = append(rdns, strings.Join(parts, "+"))
	}
	return strings.Join(rdns, ",")
}

// memberDN strips the optional unique identifier a uniqueMember value may
// carry after its DN (RFC 4517 Name and Optional UID: dn#'0101'B), leaving
// the DN to match.
func memberDN(value string) string {
	if !strings.HasSuffix(value, "'B") {
		return value
	}
	if i := strings.LastIndex(value, "#'"); i > 0 {
		return value[:i]
	}
	return value
}
