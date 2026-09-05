package service

import (
	"fmt"
	"strings"

	// text/template, not html/template: this renders SSH certificate key
	// IDs (plain metadata embedded in a cert), never HTML — HTML escaping
	// would corrupt legitimate key ID characters instead of preventing XSS.
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"text/template"

	"github.com/mnestor/ssoossh/server/config"
)

// keyIDTemplateData is the set of fields available for substitution in a
// config.CertOptions*.KeyIDTemplate. See
// https://mnestor.github.io/ssoossh/operations/key-id-templates/ — keep that
// table in sync with this struct.
//
// Extra holds the operator-configured extra claim fields (see
// config.OAuthFields.Extra), referenced as {{.Extra.name}} or {{join
// .Extra.name ";"}}. Lookups of names that were never configured render
// missingPlaceholder rather than failing: templates are parsed with
// missingkey=zero and extraValue's zero value prints MISSING.
type keyIDTemplateData struct {
	Username string
	Subject  string
	Email    string
	ClientIP string
	UniqueID string
	Extra    map[string]extraValue
}

// newKeyIDTemplateData builds the render data for one issuance. Every empty
// standard field is substituted with missingPlaceholder so a key ID shows
// an auditable gap instead of silently collapsing (extras get the same
// treatment via extraValue.String).
func newKeyIDTemplateData(identity *Identity, clientIP, uniqueID string) keyIDTemplateData {
	return keyIDTemplateData{
		Username: orMissing(identity.Username),
		Subject:  orMissing(identity.Subject),
		Email:    orMissing(identity.Email),
		ClientIP: orMissing(clientIP),
		UniqueID: orMissing(uniqueID),
		Extra:    identity.Extra,
	}
}

// orMissing substitutes missingPlaceholder for an empty string.
func orMissing(s string) string {
	if s == "" {
		return missingPlaceholder
	}
	return s
}

// Defaults used when nothing is configured at all, preserving "identity is
// the key ID" with zero configuration required.
const (
	defaultUserServiceKeyIDTemplate = "{{.Username}}"

	// defaultPAMKeyIDTemplate is PAM's own default — deliberately not a
	// fallback to defaultUserServiceKeyIDTemplate. A sudo and a login by
	// the same person must stay distinguishable in an sshd or sudo audit
	// log, so an unset PAM template identifies the type rather than
	// silently reading like a user certificate.
	defaultPAMKeyIDTemplate = "pam:{{.Username}}"

	// defaultConsoleKeyIDTemplate is console's own default, on the same
	// terms as PAM's: a console login and an SSH login by the same person
	// have to stay apart in an audit log, and a console session is the
	// bigger of the two grants.
	defaultConsoleKeyIDTemplate = "console:{{.Username}}"
)

// keyIDTemplates holds the parsed per-type key ID templates, built once at
// construction time (see newKeyIDTemplates) so a bad template — malformed
// syntax or an unrecognized field — fails startup rather than the first
// issuance.
type keyIDTemplates struct {
	user    *template.Template
	service *template.Template
	pam     *template.Template
	console *template.Template
}

// newKeyIDTemplates parses opts' per-type templates. User certificates are
// the common case, so an unset Service template falls back to whatever's
// configured for User (raw, before defaulting) — only when User's is also
// unset does each type fall back to its own hardcoded default. See
// https://mnestor.github.io/ssoossh/operations/key-id-templates/.
func newKeyIDTemplates(opts config.CertificateOptions) (*keyIDTemplates, error) {
	userSrc := opts.User.KeyIDTemplate
	if userSrc == "" {
		userSrc = defaultUserServiceKeyIDTemplate
	}
	userTmpl, err := parseKeyIDTemplate("user", userSrc)
	if err != nil {
		return nil, err
	}

	serviceSrc := opts.Service.KeyIDTemplate
	if serviceSrc == "" {
		serviceSrc = userSrc
	}
	serviceTmpl, err := parseKeyIDTemplate("service", serviceSrc)
	if err != nil {
		return nil, err
	}

	// PAM deliberately does not fall back to userSrc the way service
	// does — see defaultPAMKeyIDTemplate.
	pamSrc := opts.PAM.KeyIDTemplate
	if pamSrc == "" {
		pamSrc = defaultPAMKeyIDTemplate
	}
	pamTmpl, err := parseKeyIDTemplate("pam", pamSrc)
	if err != nil {
		return nil, err
	}

	// Console does not fall back to userSrc either — see
	// defaultConsoleKeyIDTemplate.
	consoleSrc := opts.Console.KeyIDTemplate
	if consoleSrc == "" {
		consoleSrc = defaultConsoleKeyIDTemplate
	}
	consoleTmpl, err := parseKeyIDTemplate("console", consoleSrc)
	if err != nil {
		return nil, err
	}

	return &keyIDTemplates{user: userTmpl, service: serviceTmpl, pam: pamTmpl, console: consoleTmpl}, nil
}

// parseKeyIDTemplate parses src and immediately executes it once against a
// zero-value keyIDTemplateData, so a reference to a field that doesn't exist
// (a typo, or a field
// https://mnestor.github.io/ssoossh/operations/key-id-templates/ no longer
// documents) is caught now rather than at the first real issuance —
// text/template only validates syntax at Parse time, not field names, which
// are only resolved against real data at Execute time.
func parseKeyIDTemplate(name, src string) (*template.Template, error) {
	// missingkey=zero makes an .Extra lookup of an unconfigured name render
	// the zero extraValue — which prints missingPlaceholder — instead of
	// erroring. It only affects map lookups; struct field typos still fail
	// the validation execute below. "join" renders a list-valued extra with
	// an explicit separator (String's default is a comma).
	tmpl, err := template.New(name).
		Option("missingkey=zero").
		Funcs(template.FuncMap{"join": extraValue.Join}).
		Parse(src)
	if err != nil {
		return nil, fmt.Errorf("invalid key_id_template for %s certificates: %w", name, err)
	}
	if _, err := executeKeyIDTemplate(tmpl, keyIDTemplateData{}); err != nil {
		return nil, fmt.Errorf("invalid key_id_template for %s certificates: %w", name, err)
	}
	return tmpl, nil
}

// executeKeyIDTemplate renders tmpl against data. Per-type template
// selection lives in certTypePolicy.keyIDTemplate (see certtypepolicy.go)
// instead of a switch here.
func executeKeyIDTemplate(tmpl *template.Template, data keyIDTemplateData) (string, error) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
