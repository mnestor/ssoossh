package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

// Test methodology: header grading is driven straight into checkHeaderHygiene
// with an edgeProbe that already says the app answered, so no fake network is
// needed. goodHeaders is what the app emits; each row changes one thing. The
// end-to-end drift guard, which runs the real middleware and grades against
// the values it reports, lives in diagnostics_drift_test.go.

const testHSTS = "max-age=31536000; includeSubDomains"

// testAppCSP is the app's Content-Security-Policy written out as a literal
// for the unit tests. The drift test reads the real one from the middleware.
func testAppCSP(nonce string) string {
	return "default-src 'self'; base-uri 'self'; object-src 'none'; " +
		"frame-ancestors 'none'; form-action 'self'; img-src 'self'; font-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; script-src 'self' 'nonce-" + nonce + "'"
}

// testAppHeaders is the set of values the app's middleware sends, written out
// as literals so the grading tests do not depend on the middleware package.
func testAppHeaders() AppSecurityHeaders {
	return AppSecurityHeaders{
		FrameOptions:          "DENY",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		ContentTypeOptions:    "nosniff",
		ContentSecurityPolicy: testAppCSP,
	}
}

// goodHeaders returns the header set the app emits with the default HSTS
// value. Rows copy it and change one header.
func goodHeaders() http.Header {
	return http.Header{
		"Strict-Transport-Security": {testHSTS},
		"X-Content-Type-Options":    {"nosniff"},
		"X-Frame-Options":           {"DENY"},
		"Referrer-Policy":           {"strict-origin-when-cross-origin"},
		"Content-Security-Policy":   {testAppCSP("abc")},
	}
}

// hygieneCheck grades h against the app's values and the configured HSTS value.
func hygieneCheck(t *testing.T, hsts string, h http.Header) DiagnosticCheck {
	t.Helper()
	cfg := &config.Config{}
	cfg.HTTP.Hsts = hsts
	s := NewDiagnosticsService(cfg, testAppHeaders(), nil)
	return s.checkHeaderHygiene(edgeProbe{reached: true, fromApp: true, headers: h})
}

func TestCheckHeaderHygiene_Status(t *testing.T) {
	t.Parallel()

	csp := testAppCSP("abc")

	tests := []struct {
		name   string
		mutate func(http.Header)
		want   DiagnosticStatus
	}{
		// Equal or stronger than the app.
		{name: "should be ok when every header equals the app's value", mutate: func(http.Header) {}, want: DiagnosticOK},
		{name: "should be ok when X-Frame-Options is absent and CSP frame-ancestors is 'none'", mutate: func(h http.Header) { h.Del("X-Frame-Options") }, want: DiagnosticOK},
		{name: "should be ok when X-Frame-Options is lowercase deny", mutate: func(h http.Header) { h.Set("X-Frame-Options", "deny") }, want: DiagnosticOK},
		{name: "should be ok when Referrer-Policy is strict-origin", mutate: func(h http.Header) { h.Set("Referrer-Policy", "strict-origin") }, want: DiagnosticOK},
		{name: "should be ok when Referrer-Policy is same-origin", mutate: func(h http.Header) { h.Set("Referrer-Policy", "same-origin") }, want: DiagnosticOK},
		{name: "should be ok when Referrer-Policy is no-referrer", mutate: func(h http.Header) { h.Set("Referrer-Policy", "no-referrer") }, want: DiagnosticOK},
		{name: "should be ok when Referrer-Policy is uppercase", mutate: func(h http.Header) { h.Set("Referrer-Policy", "STRICT-ORIGIN-WHEN-CROSS-ORIGIN") }, want: DiagnosticOK},
		{name: "should be ok when the last recognised Referrer-Policy token is strict", mutate: func(h http.Header) { h.Set("Referrer-Policy", "unsafe-url, strict-origin") }, want: DiagnosticOK},
		{name: "should be ok when HSTS max-age is longer and preload is added", mutate: func(h http.Header) {
			h.Set("Strict-Transport-Security", "max-age=315360000; includeSubDomains; preload")
		}, want: DiagnosticOK},
		{name: "should be ok when HSTS directive names are uppercase", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "MAX-AGE=31536000; INCLUDESUBDOMAINS") }, want: DiagnosticOK},
		{name: "should be ok when HSTS max-age is quoted", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", `max-age="31536000"; includeSubDomains`) }, want: DiagnosticOK},
		{name: "should be ok when CSP directive names are uppercase", mutate: func(h http.Header) { h.Set("Content-Security-Policy", strings.ToUpper(csp)) }, want: DiagnosticOK},
		{name: "should be ok when the script nonce differs from the app's", mutate: func(h http.Header) { h.Set("Content-Security-Policy", strings.Replace(csp, "abc", "zzz", 1)) }, want: DiagnosticOK},
		{name: "should be ok when object-src is absent but default-src is 'none'", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; script-src 'self' 'nonce-abc'")
		}, want: DiagnosticOK},

		// Weaker than the app, or missing.
		{name: "should warn when X-Frame-Options is SAMEORIGIN", mutate: func(h http.Header) { h.Set("X-Frame-Options", "SAMEORIGIN") }, want: DiagnosticWarn},
		{name: "should warn when X-Frame-Options is an unknown value", mutate: func(h http.Header) { h.Set("X-Frame-Options", "ALLOW-FROM https://a.example") }, want: DiagnosticWarn},
		{name: "should warn when CSP frame-ancestors allows self and X-Frame-Options is absent", mutate: func(h http.Header) {
			h.Del("X-Frame-Options")
			h.Set("Content-Security-Policy", strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
		}, want: DiagnosticWarn},
		{name: "should warn when CSP frame-ancestors is weaker even with X-Frame-Options DENY", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
		}, want: DiagnosticWarn},
		{name: "should warn when neither X-Frame-Options nor CSP frame-ancestors is present", mutate: func(h http.Header) {
			h.Del("X-Frame-Options")
			h.Set("Content-Security-Policy", strings.Replace(csp, "frame-ancestors 'none'; ", "", 1))
		}, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is origin-when-cross-origin", mutate: func(h http.Header) { h.Set("Referrer-Policy", "origin-when-cross-origin") }, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is origin", mutate: func(h http.Header) { h.Set("Referrer-Policy", "origin") }, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is no-referrer-when-downgrade", mutate: func(h http.Header) { h.Set("Referrer-Policy", "no-referrer-when-downgrade") }, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is unsafe-url", mutate: func(h http.Header) { h.Set("Referrer-Policy", "unsafe-url") }, want: DiagnosticWarn},
		{name: "should warn when the last recognised Referrer-Policy token is weak", mutate: func(h http.Header) { h.Set("Referrer-Policy", "strict-origin, unsafe-url") }, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is an unrecognised value", mutate: func(h http.Header) { h.Set("Referrer-Policy", "x") }, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is missing", mutate: func(h http.Header) { h.Del("Referrer-Policy") }, want: DiagnosticWarn},
		{name: "should warn when HSTS max-age is shorter than the app's", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=1; includeSubDomains") }, want: DiagnosticWarn},
		{name: "should warn when HSTS max-age is zero", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=0; includeSubDomains") }, want: DiagnosticWarn},
		{name: "should warn when HSTS drops includeSubDomains", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=31536000") }, want: DiagnosticWarn},
		{name: "should warn when HSTS is missing", mutate: func(h http.Header) { h.Del("Strict-Transport-Security") }, want: DiagnosticWarn},
		{name: "should warn when HSTS max-age is not a number", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=abc; includeSubDomains") }, want: DiagnosticWarn},
		{name: "should warn when HSTS max-age is negative", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=-1; includeSubDomains") }, want: DiagnosticWarn},
		{name: "should warn when HSTS has no max-age", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "includeSubDomains") }, want: DiagnosticWarn},
		{name: "should warn when HSTS is present but empty", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "") }, want: DiagnosticWarn},
		{name: "should warn when Content-Security-Policy is missing", mutate: func(h http.Header) { h.Del("Content-Security-Policy") }, want: DiagnosticWarn},
		{name: "should warn when CSP object-src allows self", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", strings.Replace(csp, "object-src 'none'", "object-src 'self'", 1))
		}, want: DiagnosticWarn},
		{name: "should warn when CSP script-src allows unsafe-inline", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", strings.Replace(csp, "script-src 'self' 'nonce-abc'", "script-src 'self' 'unsafe-inline'", 1))
		}, want: DiagnosticWarn},
		{name: "should warn when CSP has no object-src and no default-src", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", "frame-ancestors 'none'; script-src 'self' 'nonce-abc'")
		}, want: DiagnosticWarn},
		{name: "should warn when CSP has no script-src and no default-src", mutate: func(h http.Header) {
			h.Set("Content-Security-Policy", "object-src 'none'; frame-ancestors 'none'")
		}, want: DiagnosticWarn},
		{name: "should warn when X-Content-Type-Options is not nosniff", mutate: func(h http.Header) { h.Set("X-Content-Type-Options", "sniff") }, want: DiagnosticWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			tt.mutate(h)
			if c := hygieneCheck(t, testHSTS, h); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
			}
		})
	}
}

func TestCheckHeaderHygiene_FindingNamesHeaderAppAndObserved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(http.Header)
		want   []string
	}{
		{
			name:   "should name the header, the app's value and the edge's value for X-Frame-Options",
			mutate: func(h http.Header) { h.Set("X-Frame-Options", "SAMEORIGIN") },
			want:   []string{"X-Frame-Options", `"DENY"`, `"SAMEORIGIN"`},
		},
		{
			name:   "should name the header, the app's value and the edge's value for Referrer-Policy",
			mutate: func(h http.Header) { h.Set("Referrer-Policy", "origin-when-cross-origin") },
			want:   []string{"Referrer-Policy", `"strict-origin-when-cross-origin"`, `"origin-when-cross-origin"`},
		},
		{
			name:   "should name the header, the app's value and the edge's value for HSTS",
			mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=1") },
			want:   []string{"Strict-Transport-Security", `"max-age=31536000; includeSubDomains"`, `"max-age=1"`},
		},
		{
			name: "should name the app's framing values when neither framing control is present",
			mutate: func(h http.Header) {
				h.Del("X-Frame-Options")
				h.Set("Content-Security-Policy", strings.Replace(testAppCSP("abc"), "frame-ancestors 'none'; ", "", 1))
			},
			want: []string{"The app sets X-Frame-Options: DENY, and frame-ancestors 'none' in its CSP."},
		},
		{
			name:   "should say missing when the edge returned no Referrer-Policy",
			mutate: func(h http.Header) { h.Del("Referrer-Policy") },
			want:   []string{"Referrer-Policy", "missing", `"strict-origin-when-cross-origin"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			tt.mutate(h)
			c := hygieneCheck(t, testHSTS, h)
			joined := strings.Join(c.Findings, "\n")
			for _, w := range tt.want {
				if !strings.Contains(joined, w) {
					t.Errorf("findings %q do not contain %q", joined, w)
				}
			}
		})
	}
}

func TestCheckHeaderHygiene_HSTSDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hsts []string
		want DiagnosticStatus
	}{
		{name: "should be ok when http.hsts is empty and the edge sends none", hsts: nil, want: DiagnosticOK},
		{name: "should be ok when http.hsts is empty and the edge sends an empty header", hsts: []string{""}, want: DiagnosticOK},
		{name: "should be ok when http.hsts is empty and the edge sends a short max-age", hsts: []string{"max-age=1"}, want: DiagnosticOK},
		{name: "should be ok when http.hsts is empty and the edge sends a full policy", hsts: []string{"max-age=63072000; includeSubDomains; preload"}, want: DiagnosticOK},
		{name: "should warn when http.hsts is empty and the edge sends max-age=0", hsts: []string{"max-age=0"}, want: DiagnosticWarn},
		{name: "should warn when http.hsts is empty and the edge sends a non-numeric max-age", hsts: []string{"max-age=abc"}, want: DiagnosticWarn},
		{name: "should warn when http.hsts is empty and the edge sends no max-age", hsts: []string{"includeSubDomains"}, want: DiagnosticWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h.Del("Strict-Transport-Security")
			if tt.hsts != nil {
				h["Strict-Transport-Security"] = tt.hsts
			}
			if c := hygieneCheck(t, "", h); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
			}
		})
	}
}

func TestCheckHeaderHygiene_HSTSDisabledFindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(http.Header)
		want   string
	}{
		{name: "should say HSTS is off in config when the edge sends none", mutate: func(http.Header) {}, want: "HSTS is off in config (http.hsts is empty)"},
		{name: "should name the edge's value when it adds HSTS", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=1") }, want: `the edge adds Strict-Transport-Security "max-age=1"`},
		{name: "should say max-age=0 drops HSTS when the edge sends it", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=0") }, want: "drop any HSTS policy"},
		{name: "should say browsers ignore a malformed edge HSTS", mutate: func(h http.Header) { h.Set("Strict-Transport-Security", "max-age=x") }, want: "malformed, so browsers ignore it"},
		{name: "should keep the HSTS note when another header warns", mutate: func(h http.Header) { h.Set("X-Frame-Options", "SAMEORIGIN") }, want: "HSTS is off in config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h.Del("Strict-Transport-Security")
			tt.mutate(h)
			c := hygieneCheck(t, "", h)
			if joined := strings.Join(c.Findings, "\n"); !strings.Contains(joined, tt.want) {
				t.Errorf("findings %q do not contain %q", joined, tt.want)
			}
		})
	}
}

func TestCheckHeaderHygiene_RemediationNamesHSTSOnlyWhenConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hsts     string
		wantHSTS bool
	}{
		{name: "should name HSTS in the remediation when http.hsts is set", hsts: testHSTS, wantHSTS: true},
		{name: "should not name HSTS in the remediation when http.hsts is empty", hsts: "", wantHSTS: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h.Set("X-Frame-Options", "SAMEORIGIN")
			c := hygieneCheck(t, tt.hsts, h)
			if got := strings.Contains(c.Remediation, "HSTS"); got != tt.wantHSTS {
				t.Errorf("remediation %q names HSTS = %v, want %v", c.Remediation, got, tt.wantHSTS)
			}
		})
	}
}

func TestCheckHeaderHygiene_IncompleteAppValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*AppSecurityHeaders)
	}{
		{name: "should skip when no app values are supplied", mutate: func(a *AppSecurityHeaders) { *a = AppSecurityHeaders{} }},
		{name: "should skip when the app's X-Frame-Options value is empty", mutate: func(a *AppSecurityHeaders) { a.FrameOptions = "" }},
		{name: "should skip when the app's Referrer-Policy value is empty", mutate: func(a *AppSecurityHeaders) { a.ReferrerPolicy = "" }},
		{name: "should skip when the app's X-Content-Type-Options value is empty", mutate: func(a *AppSecurityHeaders) { a.ContentTypeOptions = "" }},
		{name: "should skip when the app's CSP builder is nil", mutate: func(a *AppSecurityHeaders) { a.ContentSecurityPolicy = nil }},
		{name: "should skip when the app's CSP builder returns an empty policy", mutate: func(a *AppSecurityHeaders) {
			a.ContentSecurityPolicy = func(string) string { return " " }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app := testAppHeaders()
			tt.mutate(&app)
			cfg := &config.Config{}
			cfg.HTTP.Hsts = testHSTS
			c := NewDiagnosticsService(cfg, app, nil).checkHeaderHygiene(edgeProbe{reached: true, fromApp: true, headers: goodHeaders()})
			if c.Status != DiagnosticSkipped {
				t.Errorf("got %q, want %q (summary: %s)", c.Status, DiagnosticSkipped, c.Summary)
			}
		})
	}
}

// withScriptSrc returns the app's CSP with script-src replaced by sources.
func withScriptSrc(sources string) string {
	return strings.Replace(testAppCSP("abc"), "script-src 'self' 'nonce-abc'", "script-src "+sources, 1)
}

func TestCheckHeaderHygiene_ScriptSrcHashSources(t *testing.T) {
	t.Parallel()

	const hash = "'sha256-B2yPHKaXnvFWtRChIbabYmUBFZdVfKKXHbWtWidDVF8='"

	tests := []struct {
		name string
		csp  string
		want DiagnosticStatus
	}{
		// A hash admits only the script it names, so it stands in for the nonce.
		{name: "should be ok when script-src uses a sha256 hash in place of the nonce", csp: withScriptSrc("'self' " + hash), want: DiagnosticOK},
		{name: "should be ok when script-src uses a sha384 hash", csp: withScriptSrc("'self' 'sha384-abc'"), want: DiagnosticOK},
		{name: "should be ok when script-src uses a sha512 hash", csp: withScriptSrc("'self' 'sha512-abc'"), want: DiagnosticOK},
		{name: "should be ok when script-src uses only a hash", csp: withScriptSrc(hash), want: DiagnosticOK},
		{name: "should be ok when script-src mixes a nonce and a hash", csp: withScriptSrc("'self' 'nonce-zzz' " + hash), want: DiagnosticOK},
		{name: "should be ok when script-src lists several hashes", csp: withScriptSrc("'self' " + hash + " 'sha384-abc'"), want: DiagnosticOK},
		{name: "should be ok when the hash prefix is uppercase", csp: withScriptSrc("'self' 'SHA256-abc'"), want: DiagnosticOK},
		{name: "should be ok when a hash comes through default-src with no script-src", csp: "default-src 'self' " + hash + "; object-src 'none'; frame-ancestors 'none'", want: DiagnosticOK},

		// Anything that loosens script matching cancels the equivalence.
		{name: "should warn when a hash comes with 'unsafe-inline'", csp: withScriptSrc("'self' " + hash + " 'unsafe-inline'"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with 'unsafe-eval'", csp: withScriptSrc("'self' " + hash + " 'unsafe-eval'"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with 'unsafe-hashes'", csp: withScriptSrc("'self' " + hash + " 'unsafe-hashes'"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with a wildcard", csp: withScriptSrc("'self' " + hash + " *"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with a wildcard host", csp: withScriptSrc("'self' " + hash + " *.example.com"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with the https: scheme source", csp: withScriptSrc("'self' " + hash + " https:"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with the data: scheme source", csp: withScriptSrc("'self' " + hash + " data:"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with 'strict-dynamic'", csp: withScriptSrc("'self' " + hash + " 'strict-dynamic'"), want: DiagnosticWarn},
		{name: "should warn when a nonce comes with 'strict-dynamic'", csp: withScriptSrc("'self' 'nonce-abc' 'strict-dynamic'"), want: DiagnosticWarn},
		{name: "should warn when a nonce and a hash come with 'unsafe-inline'", csp: withScriptSrc("'self' 'nonce-abc' " + hash + " 'unsafe-inline'"), want: DiagnosticWarn},
		{name: "should warn when a hash comes with a host the app does not allow", csp: withScriptSrc("'self' " + hash + " https://cdn.example"), want: DiagnosticWarn},

		// Malformed hashes are not hashes.
		{name: "should warn when the hash algorithm is sha1", csp: withScriptSrc("'self' 'sha1-abc'"), want: DiagnosticWarn},
		{name: "should warn when the hash value is empty", csp: withScriptSrc("'self' 'sha256-'"), want: DiagnosticWarn},
		{name: "should warn when the hash has no closing quote", csp: withScriptSrc("'self' 'sha256-abc"), want: DiagnosticWarn},
		{name: "should warn when the hash is unquoted", csp: withScriptSrc("'self' sha256-abc"), want: DiagnosticWarn},

		// The equivalence is for script-src: the app's object-src is 'none'.
		{name: "should warn when object-src carries a hash", csp: strings.Replace(testAppCSP("abc"), "object-src 'none'", "object-src "+hash, 1), want: DiagnosticWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h.Set("Content-Security-Policy", tt.csp)
			if c := hygieneCheck(t, testHSTS, h); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
			}
		})
	}
}

func TestCheckHeaderHygiene_RepeatedHeaderLines(t *testing.T) {
	t.Parallel()

	app := testAppCSP("abc")
	weakScript := withScriptSrc("'self' 'unsafe-inline'")
	noFA := strings.Replace(app, "frame-ancestors 'none'; ", "", 1)

	tests := []struct {
		name string
		csp  []string
		xfo  []string
		want DiagnosticStatus
	}{
		// Browsers enforce every CSP; one that is the app's bounds the rest.
		{name: "should be ok when a second CSP line is weak and the first is the app's", csp: []string{app, "script-src * 'unsafe-inline'; object-src *; frame-ancestors *"}, want: DiagnosticOK},
		{name: "should be ok when the weak CSP line comes first", csp: []string{"script-src * 'unsafe-inline'; object-src *; frame-ancestors *", app}, want: DiagnosticOK},
		{name: "should be ok when two comma-separated policies on one line have only one weak", csp: []string{app + ", script-src *"}, want: DiagnosticOK},
		{name: "should be ok when each CSP line is weak on a different directive", csp: []string{
			"object-src 'none'; script-src *; frame-ancestors 'none'",
			"object-src *; script-src 'self' 'nonce-abc'",
		}, want: DiagnosticOK},
		{name: "should be ok when an empty CSP line sits beside the app's", csp: []string{"", app}, want: DiagnosticOK},
		{name: "should be ok when frame-ancestors is 'none' in one line and 'self' in another", csp: []string{app, "frame-ancestors 'self'"}, want: DiagnosticOK},
		{name: "should warn when every CSP line is weak on script-src", csp: []string{weakScript, withScriptSrc("*")}, want: DiagnosticWarn},
		{name: "should warn when no single CSP line is within the app's script-src even if their intersection is", csp: []string{
			withScriptSrc("'self' https:"), withScriptSrc("'self' 'unsafe-inline'"),
		}, want: DiagnosticWarn},
		{name: "should warn when the only CSP line with frame-ancestors is weak despite X-Frame-Options DENY", csp: []string{noFA, "frame-ancestors *"}, want: DiagnosticWarn},
		{name: "should warn when every CSP line is empty", csp: []string{"", " , ; "}, want: DiagnosticWarn},

		// Every X-Frame-Options value must be the app's.
		{name: "should be ok when X-Frame-Options is DENY on two lines", csp: []string{app}, xfo: []string{"DENY", "deny"}, want: DiagnosticOK},
		{name: "should be ok when X-Frame-Options has an empty token beside DENY", csp: []string{app}, xfo: []string{"DENY,"}, want: DiagnosticOK},
		{name: "should warn when a second X-Frame-Options line is SAMEORIGIN", csp: []string{app}, xfo: []string{"DENY", "SAMEORIGIN"}, want: DiagnosticWarn},
		{name: "should warn when X-Frame-Options is a comma list of DENY and SAMEORIGIN", csp: []string{app}, xfo: []string{"DENY, SAMEORIGIN"}, want: DiagnosticWarn},
		{name: "should warn when X-Frame-Options is only a comma", csp: []string{app}, xfo: []string{","}, want: DiagnosticWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h["Content-Security-Policy"] = tt.csp
			if tt.xfo != nil {
				h["X-Frame-Options"] = tt.xfo
			}
			if c := hygieneCheck(t, testHSTS, h); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
			}
		})
	}
}

func TestCheckHeaderHygiene_RepeatedLinesFindingNamesEveryPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		csp  []string
		xfo  []string
		want string
	}{
		{name: "should list each policy's script-src when none is within the app's", csp: []string{withScriptSrc("*"), withScriptSrc("https:")}, want: `"script-src * | script-src https:"`},
		{name: "should list each policy's frame-ancestors when none is within the app's", csp: []string{
			strings.Replace(testAppCSP("abc"), "frame-ancestors 'none'", "frame-ancestors *", 1), "frame-ancestors 'self'",
		}, want: `"frame-ancestors * | frame-ancestors 'self'"`},
		{name: "should show every X-Frame-Options line joined", csp: []string{testAppCSP("abc")}, xfo: []string{"DENY", "SAMEORIGIN"}, want: `"DENY, SAMEORIGIN"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h["Content-Security-Policy"] = tt.csp
			if tt.xfo != nil {
				h["X-Frame-Options"] = tt.xfo
			}
			c := hygieneCheck(t, testHSTS, h)
			if joined := strings.Join(c.Findings, "\n"); !strings.Contains(joined, tt.want) {
				t.Errorf("findings %q do not contain %q", joined, tt.want)
			}
		})
	}
}

func TestSourceClassifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantToken   bool
		wantLoosens bool
	}{
		{name: "should treat a nonce as a token source", source: "'nonce-abc'", wantToken: true},
		{name: "should treat a sha256 hash as a token source", source: "'sha256-abc'", wantToken: true},
		{name: "should treat a sha384 hash as a token source", source: "'sha384-abc'", wantToken: true},
		{name: "should treat a sha512 hash as a token source", source: "'sha512-abc'", wantToken: true},
		{name: "should not treat a sha1 hash as a token source", source: "'sha1-abc'"},
		{name: "should not treat an empty nonce as a token source", source: "'nonce-'"},
		{name: "should not treat an empty hash as a token source", source: "'sha256-'"},
		{name: "should not treat a hash missing its closing quote as a token source", source: "'sha256-abc"},
		{name: "should not treat an empty string as a token source", source: ""},
		{name: "should not treat 'self' as loosening", source: "'self'"},
		{name: "should not treat a host as loosening", source: "https://cdn.example"},
		{name: "should treat 'unsafe-inline' as loosening", source: "'unsafe-inline'", wantLoosens: true},
		{name: "should treat 'unsafe-eval' as loosening", source: "'unsafe-eval'", wantLoosens: true},
		{name: "should treat 'unsafe-hashes' as loosening", source: "'unsafe-hashes'", wantLoosens: true},
		{name: "should treat a bare wildcard as loosening", source: "*", wantLoosens: true},
		{name: "should treat a wildcard host as loosening", source: "https://*.example", wantLoosens: true},
		{name: "should treat a scheme source as loosening", source: "blob:", wantLoosens: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isNonceOrHashSource(tt.source); got != tt.wantToken {
				t.Errorf("isNonceOrHashSource(%q) = %v, want %v", tt.source, got, tt.wantToken)
			}
			if got := loosensScripts(tt.source); got != tt.wantLoosens {
				t.Errorf("loosensScripts(%q) = %v, want %v", tt.source, got, tt.wantLoosens)
			}
		})
	}
}

func TestSourcesWithin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		observed []string
		allowed  []string
		want     bool
	}{
		{name: "should be within when observed is empty", observed: nil, allowed: []string{"'self'"}, want: true},
		{name: "should be within when observed equals allowed", observed: []string{"'self'", "'nonce-a'"}, allowed: []string{"'self'", "'nonce-a'"}, want: true},
		{name: "should match a different nonce", observed: []string{"'nonce-b'"}, allowed: []string{"'nonce-a'"}, want: true},
		{name: "should match a hash to the app's nonce", observed: []string{"'sha256-x'"}, allowed: []string{"'nonce-a'"}, want: true},
		{name: "should not match a hash when the app allows no nonce or hash", observed: []string{"'sha256-x'"}, allowed: []string{"'self'"}, want: false},
		{name: "should not match a hash when observed also has a loosening source the app allows", observed: []string{"'sha256-x'", "'unsafe-eval'"}, allowed: []string{"'nonce-a'", "'unsafe-eval'"}, want: false},
		{name: "should match a loosening source the app allows when no token equivalence is needed", observed: []string{"'nonce-a'", "'unsafe-eval'"}, allowed: []string{"'nonce-a'", "'unsafe-eval'"}, want: true},
		{name: "should not be within when a source is not allowed", observed: []string{"'self'", "https:"}, allowed: []string{"'self'"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sourcesWithin(tt.observed, tt.allowed); got != tt.want {
				t.Errorf("sourcesWithin(%q, %q) = %v, want %v", tt.observed, tt.allowed, got, tt.want)
			}
		})
	}
}
