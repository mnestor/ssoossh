package service

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// AppSecurityHeaders is the set of security response header values the app's
// own middleware sends. The header hygiene check grades the public URL's
// headers against it. server/middleware owns the values and builds this with
// middleware.AppSecurityHeaders; it is injected rather than imported because
// middleware imports this package. HSTS is not here: its value is http.hsts,
// which the service reads from config.
type AppSecurityHeaders struct {
	// FrameOptions is the X-Frame-Options value.
	FrameOptions string
	// ReferrerPolicy is the Referrer-Policy value.
	ReferrerPolicy string
	// ContentTypeOptions is the X-Content-Type-Options value.
	ContentTypeOptions string
	// ContentSecurityPolicy returns the Content-Security-Policy sent with the
	// given per-request script nonce.
	ContentSecurityPolicy func(nonce string) string
}

// complete reports whether every expected value is set. The header hygiene
// check skips when it is not, rather than grading against values it was never
// given.
func (a AppSecurityHeaders) complete() bool {
	return a.FrameOptions != "" && a.ReferrerPolicy != "" && a.ContentTypeOptions != "" &&
		a.ContentSecurityPolicy != nil && strings.TrimSpace(a.ContentSecurityPolicy(expectedNonce)) != ""
}

// expectedNonce stands in for the per-request nonce when the app's CSP is
// parsed. Nonce sources are matched by prefix, so its value is irrelevant.
const expectedNonce = "probe"

// hygieneFindings grades the security headers on the public URL against the
// values the app emits, a, and against http.hsts. a comes from the middleware
// that sets the headers, so the grading cannot drift from it. problems are
// headers that are missing or weaker than the app's; each names the header,
// the app's value and the value the edge returned. notes are informational
// and never raise the check's status. a must be complete.
func (a AppSecurityHeaders) hygieneFindings(h http.Header, hsts string) (problems, notes []string) {
	csp := cspPolicies(h)
	problems, notes = hstsFindings(h.Get("Strict-Transport-Security"), hsts)
	if v := h.Get("X-Content-Type-Options"); !strings.EqualFold(strings.TrimSpace(v), a.ContentTypeOptions) {
		problems = append(problems, weakerFinding("X-Content-Type-Options", a.ContentTypeOptions, v))
	}
	problems = append(problems, a.framingFindings(h, csp)...)
	problems = append(problems, a.cspFindings(csp)...)
	problems = append(problems, a.referrerFindings(h)...)
	// The app never sets X-XSS-Protection. Any value here is the edge's, and
	// the legacy filter is best disabled (0) rather than enabled, per current
	// guidance, since the CSP is the real control.
	if v := h.Get("X-Xss-Protection"); v != "" && !strings.HasPrefix(strings.TrimSpace(v), "0") {
		problems = append(problems, fmt.Sprintf("X-XSS-Protection is %q. The app does not set this, so an edge added it; the legacy filter should be disabled (0) or removed.", v))
	}
	return problems, notes
}

// weakerFinding words a header that is present but differs from the app's
// value in a way that loosens it.
func weakerFinding(header, app, observed string) string {
	return fmt.Sprintf("%s is weaker than the app sets. The app sets %q; the edge returned %q.", header, app, observed)
}

// missingFinding words a header the app sets that the edge did not return.
func missingFinding(header, app string) string {
	return fmt.Sprintf("%s is missing. The app sets %q.", header, app)
}

// referrerFindings grades Referrer-Policy. Repeated header lines are joined
// with commas, the way browsers combine them, and the last recognised token
// is the one that applies.
func (a AppSecurityHeaders) referrerFindings(h http.Header) []string {
	v := strings.Join(h.Values("Referrer-Policy"), ",")
	switch {
	case strings.TrimSpace(v) == "":
		return []string{missingFinding("Referrer-Policy", a.ReferrerPolicy)}
	case !a.referrerPolicyAccepted(v):
		return []string{weakerFinding("Referrer-Policy", a.ReferrerPolicy, v)}
	}
	return nil
}

// referrerPolicyAccepted reports whether a Referrer-Policy value is the app's
// policy or stricter. The last recognised token of a comma list applies; a
// value with no recognised token is not accepted.
func (a AppSecurityHeaders) referrerPolicyAccepted(value string) bool {
	app := strings.ToLower(strings.TrimSpace(a.ReferrerPolicy))
	tokens := strings.Split(value, ",")
	for i := len(tokens) - 1; i >= 0; i-- {
		switch strings.ToLower(strings.TrimSpace(tokens[i])) {
		case app, "strict-origin", "same-origin", "no-referrer":
			return true
		case "origin-when-cross-origin", "origin", "no-referrer-when-downgrade", "unsafe-url":
			return false
		}
	}
	return false
}

// framingFindings grades the framing controls. Every X-Frame-Options value,
// across repeated lines and comma lists, must be the app's value: browsers
// differ on how they resolve conflicting values, so a mix is graded as the
// weakest of them. When any CSP policy carries frame-ancestors, browsers
// ignore X-Frame-Options, and the intersection of the policies must be no
// looser than the app's frame-ancestors (see anyPolicyWithin). With neither
// control present, framing is unrestricted.
func (a AppSecurityHeaders) framingFindings(h http.Header, csp []map[string][]string) []string {
	var f []string
	xfo := strings.Join(h.Values("X-Frame-Options"), ", ")
	if strings.TrimSpace(xfo) != "" && !everyTokenIs(xfo, a.FrameOptions) {
		f = append(f, weakerFinding("X-Frame-Options", a.FrameOptions, xfo))
	}

	appFA := a.policy()["frame-ancestors"]
	within, observed, hasFA := anyPolicyWithin(csp, appFA, func(p map[string][]string) ([]string, bool) {
		v, ok := p["frame-ancestors"]
		return v, ok
	})
	switch {
	case hasFA && !within:
		f = append(f, weakerFinding("Content-Security-Policy frame-ancestors",
			"frame-ancestors "+strings.Join(appFA, " "), observedDirective("frame-ancestors", observed)))
	case strings.TrimSpace(xfo) == "" && !hasFA:
		f = append(f, fmt.Sprintf("Neither X-Frame-Options nor a CSP frame-ancestors directive is present. The app sets X-Frame-Options: %s, and frame-ancestors %s in its CSP.",
			a.FrameOptions, strings.Join(appFA, " ")))
	}
	return f
}

// everyTokenIs reports whether every comma-separated token of value equals
// want, case-insensitively. Empty tokens are skipped; a value with no
// tokens at all is not accepted.
func everyTokenIs(value, want string) bool {
	seen := false
	for _, t := range strings.Split(value, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !strings.EqualFold(t, want) {
			return false
		}
		seen = true
	}
	return seen
}

// cspFindings grades the Content-Security-Policy. object-src and script-src
// are checked against the app's policy; each falls back to default-src when
// absent, as browsers apply it. Browsers enforce every policy sent, so the
// effective policy is their intersection, graded by anyPolicyWithin. A
// missing header is a finding in its own right.
func (a AppSecurityHeaders) cspFindings(csp []map[string][]string) []string {
	if len(csp) == 0 {
		return []string{missingFinding("Content-Security-Policy", a.ContentSecurityPolicy(expectedNonce))}
	}
	app := a.policy()
	var f []string
	for _, name := range []string{"object-src", "script-src"} {
		appSrc := app[name]
		within, observed, present := anyPolicyWithin(csp, appSrc, func(p map[string][]string) ([]string, bool) {
			return effectiveSources(p, name)
		})
		if present && within {
			continue
		}
		got := "no " + name + " or default-src"
		if present {
			got = observedDirective(name, observed)
		}
		f = append(f, weakerFinding("Content-Security-Policy "+name, name+" "+strings.Join(appSrc, " "), got))
	}
	return f
}

// anyPolicyWithin grades one directive across every CSP policy the edge
// sent. A browser allows a load only when every policy allows it, so the
// effective directive is the intersection of the policies that set it. That
// intersection is no looser than allowed whenever at least one policy's
// directive is within allowed on its own, so within is true in that case.
// When no single policy is within, within is false, even where an exact
// intersection might be: computing it over host and scheme sources is not
// attempted, so the grade errs strict. present is false when no policy sets
// the directive (the resource is unrestricted). observed lists each
// policy's source list for the finding.
func anyPolicyWithin(csp []map[string][]string, allowed []string,
	directive func(map[string][]string) ([]string, bool)) (within bool, observed [][]string, present bool) {
	for _, p := range csp {
		v, ok := directive(p)
		if !ok {
			continue
		}
		present = true
		observed = append(observed, v)
		if sourcesWithin(v, allowed) {
			within = true
		}
	}
	return within, observed, present
}

// observedDirective words a directive's source lists as the edge sent them,
// one per policy that set it, joined with " | " when there are several.
func observedDirective(name string, lists [][]string) string {
	parts := make([]string, 0, len(lists))
	for _, l := range lists {
		parts = append(parts, strings.TrimSpace(name+" "+strings.Join(l, " ")))
	}
	return strings.Join(parts, " | ")
}

// policy parses the policy the app's CSP middleware sets.
func (a AppSecurityHeaders) policy() map[string][]string {
	return parseCSP(a.ContentSecurityPolicy(expectedNonce))
}

// cspPolicies returns every Content-Security-Policy the edge sent, parsed.
// Repeated header lines and comma-separated policies within a line are each
// a separate policy that browsers enforce. Empty policies are dropped, so an
// empty result means no CSP at all.
func cspPolicies(h http.Header) []map[string][]string {
	var out []map[string][]string
	for _, line := range h.Values("Content-Security-Policy") {
		for _, policy := range strings.Split(line, ",") {
			if p := parseCSP(policy); len(p) > 0 {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseCSP splits a policy into directive name to source list. Names and
// sources are lowercased, since both are case-insensitive, and the first
// occurrence of a directive wins, as browsers apply it.
func parseCSP(policy string) map[string][]string {
	out := map[string][]string{}
	for _, d := range strings.Split(policy, ";") {
		fields := strings.Fields(strings.ToLower(d))
		if len(fields) == 0 {
			continue
		}
		if _, dup := out[fields[0]]; dup {
			continue
		}
		out[fields[0]] = fields[1:]
	}
	return out
}

// effectiveSources returns the source list browsers apply for a fetch
// directive. object-src and script-src fall back to default-src when absent.
// The bool is false when neither directive is present, which leaves the
// resource unrestricted.
func effectiveSources(policy map[string][]string, name string) ([]string, bool) {
	if v, ok := policy[name]; ok {
		return v, true
	}
	if name == "object-src" || name == "script-src" {
		if v, ok := policy["default-src"]; ok {
			return v, true
		}
	}
	return nil, false
}

// sourcesWithin reports whether every observed source is one the app allows.
// An empty observed list means 'none', so it is within anything. A nonce or
// hash source is within when the app allows a nonce or hash source: the
// app's nonce differs per request, and a hash admits only the script it
// names, which is about as strict. That equivalence holds only while the
// observed list has no source that loosens script matching (see
// loosensScripts); any such source fails on its own unless the app allows
// it too.
func sourcesWithin(observed, allowed []string) bool {
	tokenEquivalent := slices.ContainsFunc(allowed, isNonceOrHashSource) &&
		!slices.ContainsFunc(observed, loosensScripts)
	for _, o := range observed {
		if slices.Contains(allowed, o) {
			continue
		}
		if tokenEquivalent && isNonceOrHashSource(o) {
			continue
		}
		return false
	}
	return true
}

// isNonceOrHashSource reports whether a CSP source expression is a nonce
// source such as 'nonce-abc' or a hash source such as 'sha256-...'. It
// expects the lowercased form parseCSP produces, and requires the closing
// quote and a non-empty value, since browsers ignore a malformed one.
func isNonceOrHashSource(source string) bool {
	for _, prefix := range []string{"'nonce-", "'sha256-", "'sha384-", "'sha512-"} {
		if strings.HasPrefix(source, prefix) && strings.HasSuffix(source, "'") && len(source) > len(prefix)+1 {
			return true
		}
	}
	return false
}

// loosensScripts reports whether a source expression widens which scripts
// run beyond what a nonce or hash admits: 'unsafe-inline', 'unsafe-eval',
// 'unsafe-hashes', a wildcard, or a scheme source such as https: or data:.
func loosensScripts(source string) bool {
	switch source {
	case "'unsafe-inline'", "'unsafe-eval'", "'unsafe-hashes'":
		return true
	}
	return strings.Contains(source, "*") || strings.HasSuffix(source, ":")
}

// hstsFindings grades Strict-Transport-Security against http.hsts, the value
// the app sends. The max-age must be at least the configured one, and
// includeSubDomains must be kept when configured. A longer max-age or preload
// is stronger and passes.
//
// An empty http.hsts means HSTS is off in config and the app sends none, so
// there is no app value to be weaker than. A missing header is then a note,
// not a problem, and says HSTS is off in config. When the edge adds HSTS of
// its own, its value is the policy browsers get: a well-formed one with a
// positive max-age is a note naming it, but a malformed one (which browsers
// ignore) or max-age=0 (which tells browsers to drop any HSTS they already
// hold) is a problem, since an edge sending either was meant to protect and
// does not.
func hstsFindings(observed, configured string) (problems, notes []string) {
	if configured == "" {
		if strings.TrimSpace(observed) == "" {
			return nil, []string{"Strict-Transport-Security (HSTS) is not sent: HSTS is off in config (http.hsts is empty), so the app sends none, and the edge adds none. If that is not intended, set http.hsts or have the edge send it."}
		}
		maxAge, _, ok := parseHSTS(observed)
		switch {
		case !ok:
			return []string{fmt.Sprintf("Strict-Transport-Security is malformed, so browsers ignore it. HSTS is off in config (http.hsts is empty), so the edge added %q.", observed)}, nil
		case maxAge == 0:
			return []string{fmt.Sprintf("Strict-Transport-Security has max-age=0, which tells browsers to drop any HSTS policy they hold. HSTS is off in config (http.hsts is empty), so the edge added %q.", observed)}, nil
		}
		return nil, []string{fmt.Sprintf("HSTS is off in config (http.hsts is empty), so the app sends none; the edge adds Strict-Transport-Security %q, and that is the policy browsers get.", observed)}
	}
	if strings.TrimSpace(observed) == "" {
		return []string{missingFinding("Strict-Transport-Security", configured)}, nil
	}
	maxAge, subdomains, ok := parseHSTS(observed)
	if !ok {
		return []string{fmt.Sprintf("Strict-Transport-Security is malformed. The app sets %q; the edge returned %q.", configured, observed)}, nil
	}
	wantAge, wantSub, _ := parseHSTS(configured)
	if maxAge < wantAge || (wantSub && !subdomains) {
		return []string{weakerFinding("Strict-Transport-Security", configured, observed)}, nil
	}
	return nil, nil
}

// parseHSTS reads max-age and includeSubDomains from an HSTS value. Directive
// names are case-insensitive and max-age may be quoted. ok is false when
// max-age is absent, not a number, or negative.
func parseHSTS(value string) (maxAge int64, subdomains, ok bool) {
	for _, d := range strings.Split(value, ";") {
		name, val, _ := strings.Cut(strings.TrimSpace(d), "=")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "max-age":
			n, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(val), `"`), 10, 64)
			if err != nil || n < 0 {
				return 0, false, false
			}
			maxAge, ok = n, true
		case "includesubdomains":
			subdomains = true
		}
	}
	return maxAge, subdomains, ok
}
