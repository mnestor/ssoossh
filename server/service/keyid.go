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
// config.CertOptions*.KeyIDTemplate. See docs/certificate-keyid-template.md
// — keep that table in sync with this struct.
type keyIDTemplateData struct {
	Username string
	Subject  string
	Email    string
	ClientIP string
	Hostname string
	UniqueID string
}

// Defaults used when nothing is configured at all, preserving "identity is
// the key ID" with zero configuration required.
const (
	defaultUserServiceKeyIDTemplate = "{{.Username}}"
	defaultHostKeyIDTemplate        = "{{.Hostname}}"

	// defaultPAMKeyIDTemplate is PAM's own default — deliberately not a
	// fallback to defaultUserServiceKeyIDTemplate. A sudo and a login by
	// the same person must stay distinguishable in an sshd or sudo audit
	// log, so an unset PAM template identifies the type rather than
	// silently reading like a user certificate.
	defaultPAMKeyIDTemplate = "pam:{{.Username}}"
)

// keyIDTemplates holds the parsed per-type key ID templates, built once at
// construction time (see newKeyIDTemplates) so a bad template — malformed
// syntax or an unrecognized field — fails startup rather than the first
// issuance.
type keyIDTemplates struct {
	user    *template.Template
	service *template.Template
	host    *template.Template
	pam     *template.Template
}

// newKeyIDTemplates parses opts' per-type templates. User certificates are
// the common case, so an unset Service or Host template falls back to
// whatever's configured for User (raw, before defaulting) — only when
// User's is also unset does each type fall back to its own hardcoded
// default. See docs/certificate-keyid-template.md.
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

	hostSrc := opts.Host.KeyIDTemplate
	if hostSrc == "" {
		hostSrc = opts.User.KeyIDTemplate
	}
	if hostSrc == "" {
		hostSrc = defaultHostKeyIDTemplate
	}
	hostTmpl, err := parseKeyIDTemplate("host", hostSrc)
	if err != nil {
		return nil, err
	}

	// PAM deliberately does not fall back to userSrc the way service and
	// host do — see defaultPAMKeyIDTemplate.
	pamSrc := opts.PAM.KeyIDTemplate
	if pamSrc == "" {
		pamSrc = defaultPAMKeyIDTemplate
	}
	pamTmpl, err := parseKeyIDTemplate("pam", pamSrc)
	if err != nil {
		return nil, err
	}

	return &keyIDTemplates{user: userTmpl, service: serviceTmpl, host: hostTmpl, pam: pamTmpl}, nil
}

// parseKeyIDTemplate parses src and immediately executes it once against a
// zero-value keyIDTemplateData, so a reference to a field that doesn't
// exist (a typo, or a field docs/certificate-keyid-template.md no longer
// documents) is caught now rather than at the first real issuance —
// text/template only validates syntax at Parse time, not field names,
// which are only resolved against real data at Execute time.
func parseKeyIDTemplate(name, src string) (*template.Template, error) {
	tmpl, err := template.New(name).Parse(src)
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
