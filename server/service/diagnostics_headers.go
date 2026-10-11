package service

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mnestor/ssoossh/server/secheaders"
)

// expectedNonce stands in for the per-request nonce when the app's CSP is
// parsed. Nonce sources are matched by prefix, so its value is irrelevant.
const expectedNonce = "probe"

// hygieneFindings grades the security headers on the public URL against the
// values ssoosshd emits. The expected values come from the same secheaders
// package the middleware uses, and from http.hsts, so they cannot drift. Each
// finding names the header, the app's value and the value the edge returned.
func hygieneFindings(h http.Header, hsts string) []string {
	csp := h.Get("Content-Security-Policy")
	var f []string
	f = append(f, hstsFindings(h.Get("Strict-Transport-Security"), hsts)...)
	if v := h.Get("X-Content-Type-Options"); !strings.EqualFold(strings.TrimSpace(v), secheaders.ContentTypeOptions) {
		f = append(f, weakerFinding("X-Content-Type-Options", secheaders.ContentTypeOptions, v))
	}
	f = append(f, framingFindings(h, csp)...)
	f = append(f, cspFindings(csp)...)
	f = append(f, referrerFindings(h)...)
	// The app never sets X-XSS-Protection. Any value here is the edge's, and
	// the legacy filter is best disabled (0) rather than enabled, per current
	// guidance, since the CSP is the real control.
	if v := h.Get("X-Xss-Protection"); v != "" && !strings.HasPrefix(strings.TrimSpace(v), "0") {
		f = append(f, fmt.Sprintf("X-XSS-Protection is %q. The app does not set this, so an edge added it; the legacy filter should be disabled (0) or removed.", v))
	}
	return f
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
func referrerFindings(h http.Header) []string {
	v := strings.Join(h.Values("Referrer-Policy"), ",")
	switch {
	case strings.TrimSpace(v) == "":
		return []string{missingFinding("Referrer-Policy", secheaders.ReferrerPolicy)}
	case !referrerPolicyAccepted(v):
		return []string{weakerFinding("Referrer-Policy", secheaders.ReferrerPolicy, v)}
	}
	return nil
}

// referrerPolicyAccepted reports whether a Referrer-Policy value is the app's
// policy or stricter. The last recognised token of a comma list applies; a
// value with no recognised token is not accepted.
func referrerPolicyAccepted(value string) bool {
	tokens := strings.Split(value, ",")
	for i := len(tokens) - 1; i >= 0; i-- {
		switch strings.ToLower(strings.TrimSpace(tokens[i])) {
		case secheaders.ReferrerPolicy, "strict-origin", "same-origin", "no-referrer":
			return true
		case "origin-when-cross-origin", "origin", "no-referrer-when-downgrade", "unsafe-url":
			return false
		}
	}
	return false
}

// framingFindings grades the framing controls. X-Frame-Options, when set,
// must be the app's value. When it is absent, CSP frame-ancestors must carry
// the app's restriction, since browsers ignore X-Frame-Options alongside it.
func framingFindings(h http.Header, csp string) []string {
	var f []string
	xfo := h.Get("X-Frame-Options")
	if xfo != "" && !strings.EqualFold(strings.TrimSpace(xfo), secheaders.FrameOptions) {
		f = append(f, weakerFinding("X-Frame-Options", secheaders.FrameOptions, xfo))
	}

	appFA := appCSP()["frame-ancestors"]
	fa, hasFA := parseCSP(csp)["frame-ancestors"]
	switch {
	case hasFA && !sourcesWithin(fa, appFA):
		f = append(f, weakerFinding("Content-Security-Policy frame-ancestors",
			"frame-ancestors "+strings.Join(appFA, " "), "frame-ancestors "+strings.Join(fa, " ")))
	case xfo == "" && !hasFA:
		f = append(f, "Neither X-Frame-Options nor a CSP frame-ancestors directive is present. The app sets X-Frame-Options: DENY, and frame-ancestors 'none' in its CSP.")
	}
	return f
}

// cspFindings grades the Content-Security-Policy. object-src and script-src
// are checked against the app's policy; each falls back to default-src when
// absent, as browsers apply it. A missing header is a finding in its own right.
func cspFindings(csp string) []string {
	if strings.TrimSpace(csp) == "" {
		return []string{missingFinding("Content-Security-Policy", secheaders.ContentSecurityPolicy(expectedNonce))}
	}
	app := appCSP()
	obs := parseCSP(csp)
	var f []string
	for _, name := range []string{"object-src", "script-src"} {
		appSrc := app[name]
		got, ok := effectiveSources(obs, name)
		if ok && sourcesWithin(got, appSrc) {
			continue
		}
		observed := "no " + name + " or default-src"
		if ok {
			observed = name + " " + strings.Join(got, " ")
		}
		f = append(f, weakerFinding("Content-Security-Policy "+name, name+" "+strings.Join(appSrc, " "), observed))
	}
	return f
}

// appCSP parses the policy the app's CSP middleware sets.
func appCSP() map[string][]string {
	return parseCSP(secheaders.ContentSecurityPolicy(expectedNonce))
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
// An empty observed list means 'none', so it is within anything. Nonce
// sources match any nonce, since the app's nonce differs per request.
func sourcesWithin(observed, allowed []string) bool {
	for _, o := range observed {
		found := false
		for _, a := range allowed {
			if a == o || (secheaders.IsNonceSource(o) && secheaders.IsNonceSource(a)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// hstsFindings grades Strict-Transport-Security against http.hsts, the value
// the app sends. The max-age must be at least the configured one, and
// includeSubDomains must be kept when configured. A longer max-age or preload
// is stronger and passes. An empty http.hsts means the app sends none, so a
// missing header still warns, as it always has, but a present one is not
// graded.
func hstsFindings(observed, configured string) []string {
	if configured == "" {
		if observed == "" {
			return []string{"Strict-Transport-Security (HSTS) is missing. http.hsts is empty, so the app sends none; set it or let the edge send one."}
		}
		return nil
	}
	if strings.TrimSpace(observed) == "" {
		return []string{missingFinding("Strict-Transport-Security", configured)}
	}
	maxAge, subdomains, ok := parseHSTS(observed)
	if !ok {
		return []string{fmt.Sprintf("Strict-Transport-Security is malformed. The app sets %q; the edge returned %q.", configured, observed)}
	}
	wantAge, wantSub, _ := parseHSTS(configured)
	var f []string
	if maxAge < wantAge || (wantSub && !subdomains) {
		f = append(f, weakerFinding("Strict-Transport-Security", configured, observed))
	}
	return f
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
