package service

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	// LDAP filters are a plain-text grammar, not HTML; html/template's
	// escaping would corrupt them, and RFC 4515 escaping is applied to
	// every interpolated value instead (see filterTemplate).
	"text/template" // nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/config"
)

// ldapConn is the directory operations the enrichment and sync paths need,
// behind an interface so both can be tested without a live server.
type ldapConn interface {
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// ldapDialer opens an authenticated connection to the directory. Named so
// tests can substitute one; production uses dialLDAP.
type ldapDialer func(cfg *config.LDAPConfig) (ldapConn, error)

// ldapConnAdapter narrows *ldap.Conn to ldapConn, whose Close returns an
// error the caller can log rather than the library's bare signature.
type ldapConnAdapter struct{ conn *ldap.Conn }

func (a *ldapConnAdapter) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	return a.conn.Search(req)
}

func (a *ldapConnAdapter) Close() error { return a.conn.Close() }

// dialLDAP connects, optionally upgrades to TLS, and binds. Every step is
// bounded by cfg.Timeout, since the login callback is a user-facing path.
func dialLDAP(cfg *config.LDAPConfig) (ldapConn, error) {
	tlsConfig, err := ldapTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	conn, err := ldap.DialURL(cfg.URL, ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the directory: %w", err)
	}
	conn.SetTimeout(cfg.Timeout)

	// StartTLS upgrades a plain ldap:// connection. An ldaps:// URL is
	// already TLS, so asking for both is a config mistake rather than a
	// double wrap; the library reports it.
	if cfg.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed to start TLS on the directory connection: %w", err)
		}
	}

	// An empty bind DN is an anonymous bind, which some directories permit
	// for reads. Deliberately allowed rather than required.
	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed to bind to the directory: %w", err)
		}
	}

	return &ldapConnAdapter{conn: conn}, nil
}

// ldapTLSConfig builds the TLS settings for the directory connection.
func ldapTLSConfig(cfg *config.LDAPConfig) (*tls.Config, error) {
	// #nosec G402 -- InsecureSkipVerify is an explicit, documented operator
	// choice (ldap.tls_insecure_skip_verify) that warns loudly at startup.
	out := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.TLSInsecureSkipVerify,
	}
	if cfg.TLSCA == "" {
		return out, nil
	}

	pem, err := os.ReadFile(cfg.TLSCA)
	if err != nil {
		return nil, fmt.Errorf("failed to read ldap.tls_ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ldap.tls_ca %q contains no usable certificates", cfg.TLSCA)
	}
	out.RootCAs = pool
	return out, nil
}

// filterTemplate is a parsed LDAP filter template. Every interpolated value
// is RFC 4515 escaped during execution and the operator cannot opt out: a
// preferred_username containing * or ) is otherwise filter injection, and
// offering escaping as a template function would make the safe path the one
// you have to remember.
type filterTemplate struct {
	tmpl *template.Template
	// referencedAttrs are the primary-entry attribute names the template
	// reads through .Attr. The primary lookup requests them automatically,
	// so nothing has to be duplicated as an extra field.
	referencedAttrs []string
}

// filterData is what a filter template renders against.
type filterData struct {
	Username string
	Email    string
	Subject  string
	// DN is the primary entry's distinguished name, for ownership links
	// like owner={{.DN}}. Empty on the primary filter itself.
	DN string
	// Extra is the OIDC extra claim map, rendered as scalars.
	Extra map[string]string
	// Attr is the primary entry's attributes, for reverse links keyed by
	// something other than the username. Empty on the primary filter.
	Attr map[string]string
}

// parseFilterTemplate parses one filter, wrapping every value in RFC 4515
// escaping, and records which .Attr names it reads.
func parseFilterTemplate(name, text string) (*filterTemplate, error) {
	// The escaping is applied by a pipeline function injected around every
	// action, rather than trusted to the template author.
	tmpl, err := template.New(name).
		Funcs(template.FuncMap{"ldapEscape": ldap.EscapeFilter}).
		Option("missingkey=zero").
		Parse(wrapFilterActions(text))
	if err != nil {
		return nil, fmt.Errorf("failed to parse the LDAP filter %q: %w", name, err)
	}
	return &filterTemplate{tmpl: tmpl, referencedAttrs: referencedAttrNames(text)}, nil
}

// wrapFilterActions rewrites `{{X}}` into `{{ldapEscape (X)}}` so escaping
// cannot be forgotten or bypassed. Actions that already call ldapEscape are
// left alone, and non-value actions (comments) are skipped.
func wrapFilterActions(text string) string {
	var out strings.Builder
	rest := text
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			out.WriteString(rest)
			return out.String()
		}
		close := strings.Index(rest[open:], "}}")
		if close < 0 {
			// Unterminated action: hand it to the parser, which reports a
			// better error than anything invented here.
			out.WriteString(rest)
			return out.String()
		}
		close += open

		out.WriteString(rest[:open])
		inner := strings.TrimSpace(rest[open+2 : close])
		switch {
		case inner == "" || strings.HasPrefix(inner, "/*"):
			out.WriteString(rest[open : close+2])
		case strings.HasPrefix(inner, "ldapEscape"):
			out.WriteString(rest[open : close+2])
		default:
			out.WriteString("{{ldapEscape (" + inner + ")}}")
		}
		rest = rest[close+2:]
	}
}

// referencedAttrNames collects the attribute names a template reads through
// .Attr, in either dotted or index form, so the primary lookup can request
// them.
func referencedAttrNames(text string) []string {
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}

	rest := text
	for {
		i := strings.Index(rest, ".Attr")
		if i < 0 {
			return names
		}
		rest = rest[i+len(".Attr"):]
		switch {
		case strings.HasPrefix(rest, "."):
			// .Attr.employeeNumber
			end := strings.IndexAny(rest[1:], " \t)}\"")
			if end < 0 {
				add(rest[1:])
				return names
			}
			add(rest[1 : 1+end])
		case strings.HasPrefix(rest, " \""), strings.HasPrefix(rest, "\""):
			// index .Attr "employee-number"
			q := strings.Index(rest, "\"")
			endq := strings.Index(rest[q+1:], "\"")
			if endq < 0 {
				return names
			}
			add(rest[q+1 : q+1+endq])
		}
	}
}

// execute renders the filter for data.
func (f *filterTemplate) execute(data filterData) (string, error) {
	var buf bytes.Buffer
	if err := f.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render the LDAP filter: %w", err)
	}
	filter := buf.String()
	// A filter that failed to interpolate anything meaningful would match
	// far too much, so it is refused rather than sent.
	if strings.TrimSpace(filter) == "" {
		return "", fmt.Errorf("the LDAP filter rendered empty")
	}
	return filter, nil
}

// reduceGroupName turns a directory group value into the comparable name
// the allowlist matches against. memberOf yields DNs, so a value that
// parses as one is reduced to its first RDN value — conventionally the CN.
// Anything that is not a DN is already a name and is kept as-is.
func reduceGroupName(value string) string {
	dn, err := ldap.ParseDN(value)
	if err != nil || len(dn.RDNs) == 0 || len(dn.RDNs[0].Attributes) == 0 {
		return value
	}
	return dn.RDNs[0].Attributes[0].Value
}
