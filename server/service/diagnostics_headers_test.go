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
		hsts http.Header
		want DiagnosticStatus
	}{
		{name: "should warn when http.hsts is empty and the edge sends none", hsts: nil, want: DiagnosticWarn},
		{name: "should not grade HSTS when http.hsts is empty and the edge sends one", hsts: http.Header{"Strict-Transport-Security": {"max-age=1"}}, want: DiagnosticOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := goodHeaders()
			h.Del("Strict-Transport-Security")
			for k, v := range tt.hsts {
				h[k] = v
			}
			if c := hygieneCheck(t, "", h); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
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
