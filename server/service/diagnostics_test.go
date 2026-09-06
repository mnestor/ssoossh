package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
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
	err     error
}

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
	return &http.Response{
		StatusCode: r.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("")),
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

	tests := []struct {
		name    string
		trusted []string
		want    DiagnosticStatus
	}{
		{name: "should be critical when a default route is trusted", trusted: []string{"0.0.0.0/0"}, want: DiagnosticCritical},
		{name: "should be critical when the ipv6 default route is trusted", trusted: []string{"::/0"}, want: DiagnosticCritical},
		{name: "should warn when no proxy is trusted", trusted: nil, want: DiagnosticWarn},
		{name: "should be ok when a specific proxy is trusted", trusted: []string{"10.0.10.1/32"}, want: DiagnosticOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith("https://x.example", tt.trusted), &fakeDoer{})
			got := s.Run(context.Background(), DiagnosticsRequest{
				RemoteAddr: "10.0.10.1:5000", ClientIP: "203.0.113.9",
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
			healthURL: {status: 200, headers: http.Header{}},
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
		"Strict-Transport-Security": {"max-age=63072000"},
		"X-Content-Type-Options":    {"nosniff"},
		"X-Frame-Options":           {"DENY"},
		"Referrer-Policy":           {"strict-origin-when-cross-origin"},
	}

	tests := []struct {
		name string
		hdr  http.Header
		want DiagnosticStatus
	}{
		{name: "should be ok when every expected header is present", hdr: good, want: DiagnosticOK},
		{name: "should warn when HSTS is missing", hdr: http.Header{"X-Content-Type-Options": {"nosniff"}, "X-Frame-Options": {"DENY"}, "Referrer-Policy": {"x"}}, want: DiagnosticWarn},
		{
			name: "should warn when a legacy X-XSS-Protection is present",
			hdr: http.Header{
				"Strict-Transport-Security": {"max-age=1"}, "X-Content-Type-Options": {"nosniff"},
				"X-Frame-Options": {"DENY"}, "Referrer-Policy": {"x"}, "X-Xss-Protection": {"1; mode=block"},
			},
			want: DiagnosticWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewDiagnosticsService(cfgWith(origin, nil), &fakeDoer{byURL: map[string]fakeResp{
				healthURL: {status: 200, headers: tt.hdr},
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
