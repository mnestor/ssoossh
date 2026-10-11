package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/secheaders"
)

// Test methodology: the edge checks are driven by a fake httpDoer that
// returns canned responses per URL, so nothing touches the network. The
// proxy-trust check needs no probe and is driven by config plus a
// DiagnosticsRequest. Table-driven per classification.

// fakeDoer returns a canned response (and/or error) keyed by request URL.
type fakeDoer struct {
	byURL map[string]fakeResp
}

type fakeResp struct {
	status  int
	headers http.Header
	body    string
	// bodyErr, when set, fails the body read after body has been returned.
	bodyErr error
	err     error
}

// failingReader returns its error on every read.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// appHealthz is ssoosshd's own /healthz body, which the probe needs to see
// before it treats the response as the app's.
const appHealthz = `{"status":"ok"}`

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	r, ok := f.byURL[req.URL.String()]
	if !ok {
		return nil, io.EOF
	}
	if r.err != nil {
		return nil, r.err
	}
	h := r.headers
	if h == nil {
		h = http.Header{}
	}
	var body io.Reader = strings.NewReader(r.body)
	if r.bodyErr != nil {
		body = io.MultiReader(body, failingReader{r.bodyErr})
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     h,
		Body:       io.NopCloser(body),
	}, nil
}

func cfgWith(publicURL string, trusted []string) *config.Config {
	c := &config.Config{}
	c.HTTP.PublicURL = publicURL
	c.HTTP.TrustedProxies = trusted
	return c
}

func findCheck(report DiagnosticsReport, id string) DiagnosticCheck {
	for _, c := range report.Checks {
		if c.ID == id {
			return c
		}
	}
	return DiagnosticCheck{}
}

func TestDiagnostics_ProxyTrust(t *testing.T) {
	t.Parallel()

	// An empty trusted list is only a warning when something looks to be in
	// front of the server, so the request shape matters as much as the
	// config: peer == resolved client IP with no X-Forwarded-For is a
	// directly exposed server, which is the correct configuration.
	tests := []struct {
		name         string
		trusted      []string
		remoteAddr   string
		clientIP     string
		forwardedFor string
		want         DiagnosticStatus
	}{
		{
			name:       "should be critical when a default route is trusted",
			trusted:    []string{"0.0.0.0/0"},
			remoteAddr: "10.0.10.1:5000", clientIP: "203.0.113.9",
			want: DiagnosticCritical,
		},
		{
			name:       "should be critical when the ipv6 default route is trusted",
			trusted:    []string{"::/0"},
			remoteAddr: "10.0.10.1:5000", clientIP: "203.0.113.9",
			want: DiagnosticCritical,
		},
		{
			name:       "should be ok when a specific proxy is trusted",
			trusted:    []string{"10.0.10.1/32"},
			remoteAddr: "10.0.10.1:5000", clientIP: "203.0.113.9",
			want: DiagnosticOK,
		},
		{
			name:       "should be ok when nothing is trusted and nothing sits in front",
			remoteAddr: "203.0.113.9:5000", clientIP: "203.0.113.9",
			want: DiagnosticOK,
		},
		{
			name:       "should warn when nothing is trusted but a forwarded-for arrives",
			remoteAddr: "203.0.113.9:5000", clientIP: "203.0.113.9", forwardedFor: "198.51.100.4",
			want: DiagnosticWarn,
		},
		{
			name:       "should warn when nothing is trusted and the peer is not the resolved client",
			remoteAddr: "10.0.10.1:5000", clientIP: "203.0.113.9",
			want: DiagnosticWarn,
		},
		{
			name:     "should warn when nothing is trusted and the peer cannot be parsed",
			clientIP: "203.0.113.9",
			want:     DiagnosticWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith("https://x.example", tt.trusted), &fakeDoer{})
			got := s.Run(context.Background(), DiagnosticsRequest{
				RemoteAddr: tt.remoteAddr, ClientIP: tt.clientIP, ForwardedFor: tt.forwardedFor,
			})
			if c := findCheck(got, "proxy_trust"); c.Status != tt.want {
				t.Errorf("got %q, want %q (summary: %s)", c.Status, tt.want, c.Summary)
			}
		})
	}
}

func TestDiagnostics_CORS(t *testing.T) {
	t.Parallel()

	const origin = "https://x.example"
	corsURL := origin + "/.well-known/openid-configuration"
	healthURL := origin + "/healthz"

	corsCase := func(h http.Header) *fakeDoer {
		return &fakeDoer{byURL: map[string]fakeResp{
			healthURL: {status: 200, headers: http.Header{}, body: appHealthz},
			corsURL:   {status: 200, headers: h},
		}}
	}

	tests := []struct {
		name string
		hdr  http.Header
		want DiagnosticStatus
	}{
		{
			name: "should be critical when an arbitrary origin is reflected with credentials",
			hdr:  http.Header{"Access-Control-Allow-Origin": {canaryOrigin}, "Access-Control-Allow-Credentials": {"true"}},
			want: DiagnosticCritical,
		},
		{
			name: "should warn when credentials are allowed with a wildcard origin",
			hdr:  http.Header{"Access-Control-Allow-Origin": {"*"}, "Access-Control-Allow-Credentials": {"true"}},
			want: DiagnosticWarn,
		},
		{
			name: "should warn when credentials are allowed at all",
			hdr:  http.Header{"Access-Control-Allow-Credentials": {"true"}},
			want: DiagnosticWarn,
		},
		{
			name: "should be ok when only the app's wildcard is present",
			hdr:  http.Header{"Access-Control-Allow-Origin": {"*"}},
			want: DiagnosticOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith(origin, nil), corsCase(tt.hdr))
			got := s.Run(context.Background(), DiagnosticsRequest{})
			if c := findCheck(got, "cors"); c.Status != tt.want {
				t.Errorf("got %q, want %q (summary: %s)", c.Status, tt.want, c.Summary)
			}
		})
	}
}

func TestDiagnostics_HeaderHygiene(t *testing.T) {
	t.Parallel()

	const origin = "https://x.example"
	healthURL := origin + "/healthz"

	good := http.Header{
		"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"},
		"X-Content-Type-Options":    {"nosniff"},
		"X-Frame-Options":           {"DENY"},
		"Referrer-Policy":           {"strict-origin-when-cross-origin"},
		"Content-Security-Policy":   {secheaders.ContentSecurityPolicy("abc")},
	}

	tests := []struct {
		name string
		hdr  http.Header
		want DiagnosticStatus
	}{
		{name: "should be ok when every expected header is present", hdr: good, want: DiagnosticOK},
		{
			name: "should be ok when a CSP frame-ancestors stands in for X-Frame-Options",
			hdr: http.Header{
				"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"nosniff"},
				"Content-Security-Policy": {"default-src 'none'; frame-ancestors 'none'; script-src 'self' 'nonce-abc'"}, "Referrer-Policy": {"strict-origin-when-cross-origin"},
			},
			want: DiagnosticOK,
		},
		{name: "should warn when HSTS is missing", hdr: http.Header{"X-Content-Type-Options": {"nosniff"}, "X-Frame-Options": {"DENY"}, "Referrer-Policy": {"strict-origin-when-cross-origin"}}, want: DiagnosticWarn},
		{name: "should warn when X-Content-Type-Options is not nosniff", hdr: http.Header{"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"sniff"}, "X-Frame-Options": {"DENY"}, "Referrer-Policy": {"strict-origin-when-cross-origin"}}, want: DiagnosticWarn},
		{name: "should warn when neither X-Frame-Options nor frame-ancestors is present", hdr: http.Header{"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"nosniff"}, "Referrer-Policy": {"strict-origin-when-cross-origin"}}, want: DiagnosticWarn},
		{name: "should warn when Referrer-Policy is missing", hdr: http.Header{"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"nosniff"}, "X-Frame-Options": {"DENY"}}, want: DiagnosticWarn},
		{
			name: "should warn when a legacy X-XSS-Protection is present",
			hdr: http.Header{
				"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"nosniff"},
				"X-Frame-Options": {"DENY"}, "Referrer-Policy": {"strict-origin-when-cross-origin"}, "X-Xss-Protection": {"1; mode=block"},
			},
			want: DiagnosticWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith(origin, nil), &fakeDoer{byURL: map[string]fakeResp{
				healthURL: {status: 200, headers: tt.hdr, body: appHealthz},
			}})
			got := s.Run(context.Background(), DiagnosticsRequest{})
			if c := findCheck(got, "header_hygiene"); c.Status != tt.want {
				t.Errorf("got %q, want %q (findings: %v)", c.Status, tt.want, c.Findings)
			}
		})
	}
}

func TestDiagnostics_ReachabilityWarnsWhenPublicURLUnset(t *testing.T) {
	t.Parallel()

	s := NewDiagnosticsService(cfgWith("", nil), &fakeDoer{})
	got := s.Run(context.Background(), DiagnosticsRequest{})

	if c := findCheck(got, "reachability"); c.Status != DiagnosticWarn {
		t.Errorf("got %q, want warn", c.Status)
	}
	// The edge checks cannot run without a URL, so they skip rather than
	// showing a false all-clear.
	if c := findCheck(got, "cors"); c.Status != DiagnosticSkipped {
		t.Errorf("cors should be skipped, got %q", c.Status)
	}
	if c := findCheck(got, "header_hygiene"); c.Status != DiagnosticSkipped {
		t.Errorf("header hygiene should be skipped, got %q", c.Status)
	}
}

func TestDiagnostics_ReachabilitySkipsWhenUnreachable(t *testing.T) {
	t.Parallel()

	// No URL registered in the fake, so the probe fails.
	s := NewDiagnosticsService(cfgWith("https://unreachable.example", nil), &fakeDoer{})
	got := s.Run(context.Background(), DiagnosticsRequest{})

	if c := findCheck(got, "reachability"); c.Status != DiagnosticSkipped {
		t.Errorf("got %q, want skipped", c.Status)
	}
}

func TestDiagnostics_ReachabilityWhoAnswered(t *testing.T) {
	t.Parallel()

	const origin = "https://x.example"
	healthURL := origin + "/healthz"
	corsURL := origin + "/.well-known/openid-configuration"
	edgeHeaders := http.Header{
		"Strict-Transport-Security": {"max-age=31536000; includeSubDomains"}, "X-Content-Type-Options": {"nosniff"},
		"X-Frame-Options": {"DENY"}, "Referrer-Policy": {"strict-origin-when-cross-origin"},
		"Content-Security-Policy": {secheaders.ContentSecurityPolicy("abc")},
	}

	// The second and third cases are the edge answering /healthz itself: a
	// dev server's SPA 404 (what vite dev returns, since it proxies only
	// /api, /auth and /.well-known) and a proxy that serves health paths
	// with a bare 200.
	tests := []struct {
		name       string
		health     fakeResp
		wantReach  DiagnosticStatus
		wantHeader DiagnosticStatus
	}{
		{
			name:       "should be ok and grade headers when ssoosshd answers healthz",
			health:     fakeResp{status: 200, headers: edgeHeaders, body: appHealthz},
			wantReach:  DiagnosticOK,
			wantHeader: DiagnosticOK,
		},
		{
			name:       "should warn and skip headers when the edge answers healthz with a 404 page",
			health:     fakeResp{status: 404, headers: edgeHeaders, body: "<!doctype html><html></html>"},
			wantReach:  DiagnosticWarn,
			wantHeader: DiagnosticSkipped,
		},
		{
			name:       "should warn and skip headers when the healthz body is cut off part-way",
			health:     fakeResp{status: 200, headers: edgeHeaders, body: appHealthz, bodyErr: io.ErrUnexpectedEOF},
			wantReach:  DiagnosticWarn,
			wantHeader: DiagnosticSkipped,
		},
		{
			name:       "should warn and skip headers when the edge answers healthz with its own 200",
			health:     fakeResp{status: 200, headers: edgeHeaders, body: "OK"},
			wantReach:  DiagnosticWarn,
			wantHeader: DiagnosticSkipped,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith(origin, nil), &fakeDoer{byURL: map[string]fakeResp{
				healthURL: tt.health,
				corsURL:   {status: 200, headers: http.Header{"Access-Control-Allow-Origin": {"*"}}},
			}})
			got := s.Run(context.Background(), DiagnosticsRequest{})
			if c := findCheck(got, "reachability"); c.Status != tt.wantReach {
				t.Errorf("reachability got %q, want %q (summary: %s)", c.Status, tt.wantReach, c.Summary)
			}
			if c := findCheck(got, "header_hygiene"); c.Status != tt.wantHeader {
				t.Errorf("header hygiene got %q, want %q (summary: %s)", c.Status, tt.wantHeader, c.Summary)
			}
		})
	}
}

func TestDiagnostics_CORSRunsWhenTheEdgeAnswersHealthz(t *testing.T) {
	t.Parallel()

	// The CORS probe goes to /.well-known, which even a dev server routes to
	// the app, so an edge-answered /healthz must not take it down too.
	const origin = "https://x.example"
	s := NewDiagnosticsService(cfgWith(origin, nil), &fakeDoer{byURL: map[string]fakeResp{
		origin + "/healthz": {status: 404, body: "<!doctype html>"},
		origin + "/.well-known/openid-configuration": {
			status:  200,
			headers: http.Header{"Access-Control-Allow-Credentials": {"true"}},
		},
	}})
	got := s.Run(context.Background(), DiagnosticsRequest{})

	if c := findCheck(got, "cors"); c.Status != DiagnosticWarn {
		t.Errorf("got %q, want warn (summary: %s)", c.Status, c.Summary)
	}
}

func TestIsAppHealthz(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "should accept the app's 200 with status ok", status: 200, body: appHealthz, want: true},
		{name: "should accept the app's body with surrounding whitespace", status: 200, body: " {\"status\":\"ok\"}\n", want: true},
		{name: "should reject the app's body on a non-200 status", status: 404, body: appHealthz},
		{name: "should reject an empty body", status: 200, body: ""},
		{name: "should reject a plain-text OK", status: 200, body: "OK"},
		{name: "should reject an html page", status: 200, body: "<!doctype html>"},
		{name: "should reject json with another status", status: 200, body: `{"status":"down"}`},
		{name: "should reject json without a status field", status: 200, body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isAppHealthz(tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
