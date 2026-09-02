package config

// Test methodology: table-driven against HTTPSettings values directly, no
// viper and no file loading — these are pure functions over configuration,
// and the thing worth pinning is what a given combination of settings
// resolves to.
//
// PublicOrigin, PublicHost, and IsTLS decide the OIDC redirect URI, the
// Host check, and the session cookie's Secure attribute. All are things a
// deployment gets wrong once, silently, and then spends an afternoon on.

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config/tlsutils"
)

func TestPublicOriginShouldResolveTheBrowserVisibleOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		http HTTPSettings
		want string
	}{
		{
			name: "should use public_url when it is set",
			http: HTTPSettings{PublicURL: "https://ssh.example.com"},
			want: "https://ssh.example.com",
		},
		{
			name: "should tolerate a trailing slash on public_url",
			http: HTTPSettings{PublicURL: "https://ssh.example.com/"},
			want: "https://ssh.example.com",
		},
		{
			name: "should keep a non-default port given in public_url",
			http: HTTPSettings{PublicURL: "https://ssh.example.com:8443"},
			want: "https://ssh.example.com:8443",
		},
		// The case this setting exists for: a proxy terminates TLS on 443
		// while this process listens on plain HTTP somewhere else. The
		// listen port must never leak into the origin.
		{
			name: "should ignore the listen port when public_url is set",
			http: HTTPSettings{PublicURL: "https://ssh.example.com", Port: 8080},
			want: "https://ssh.example.com",
		},
		{
			name: "should keep an http public_url as http",
			http: HTTPSettings{PublicURL: "http://ssh.internal"},
			want: "http://ssh.internal",
		},
		// Nothing to go on. Callers read "" as "unknown" rather than guessing.
		{
			name: "should return empty when public_url is not set",
			http: HTTPSettings{Port: 8080},
			want: "",
		},
		{
			name: "should return empty when public_url is only whitespace",
			http: HTTPSettings{PublicURL: "   "},
			want: "",
		},
		// An unparseable value is rejected at startup by Validate; if one
		// reaches here anyway, "unknown" beats returning a broken origin.
		{
			name: "should return empty when public_url is invalid",
			http: HTTPSettings{PublicURL: "ssh.example.com", Port: 80},
			want: "",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.http.PublicOrigin(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPublicHostShouldResolveTheNameRequestsMustBeAddressedTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		http HTTPSettings
		want string
	}{
		{
			name: "should return the host of public_url",
			http: HTTPSettings{PublicURL: "https://ssh.example.com"},
			want: "ssh.example.com",
		},
		// The middleware compares names only; behind a proxy the browser's
		// port and the listen port differ and neither identifies the host.
		{
			name: "should drop the port from public_url",
			http: HTTPSettings{PublicURL: "https://ssh.example.com:8443"},
			want: "ssh.example.com",
		},
		{
			name: "should keep an ip literal host",
			http: HTTPSettings{PublicURL: "http://203.0.113.9:8080"},
			want: "203.0.113.9",
		},
		{
			name: "should unbracket an ipv6 literal host",
			http: HTTPSettings{PublicURL: "http://[::1]:8080"},
			want: "::1",
		},
		// Empty disables the Host check in middleware.ServerNameMiddleware.
		{
			name: "should return empty when public_url is not set",
			http: HTTPSettings{},
			want: "",
		},
		{
			name: "should return empty when public_url is invalid",
			http: HTTPSettings{PublicURL: "ssh.example.com"},
			want: "",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.http.PublicHost(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTLSShouldReportTheBrowserVisibleScheme(t *testing.T) {
	t.Parallel()

	// The files need not exist — HasKeyPair only checks that both paths are
	// set, and IsTLS asks nothing more than that.
	withKeyPair := tlsutils.TLSConfig{CertificateInfo: tlsutils.CertificateInfo{
		CertificateFile: "server.crt",
		PrivateKeyFile:  "server.key",
	}}

	tests := []struct {
		name string
		http HTTPSettings
		want bool
	}{
		{name: "should report true for an https public_url", http: HTTPSettings{PublicURL: "https://ssh.example.com"}, want: true},
		{name: "should report false for an http public_url", http: HTTPSettings{PublicURL: "http://ssh.example.com"}, want: false},
		// public_url describes what the browser sees, so it settles the
		// question even when this process also holds a certificate.
		{name: "should let an http public_url override a local keypair", http: HTTPSettings{PublicURL: "http://ssh.internal", TLS: withKeyPair}, want: false},
		{name: "should report true when this process terminates tls", http: HTTPSettings{TLS: withKeyPair}, want: true},
		{name: "should fall back to the keypair when public_url is invalid", http: HTTPSettings{PublicURL: "ssh.example.com", TLS: withKeyPair}, want: true},
		{name: "should report false with nothing configured", http: HTTPSettings{}, want: false},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.http.IsTLS(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateShouldRejectUnusablePublicURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		publicURL string
		wantErr   string
	}{
		{name: "should accept an empty public_url", publicURL: ""},
		{name: "should accept an origin", publicURL: "https://ssh.example.com"},
		{name: "should accept an origin with a port", publicURL: "https://ssh.example.com:8443"},
		{name: "should accept a trailing slash", publicURL: "https://ssh.example.com/"},
		{name: "should reject a missing scheme", publicURL: "ssh.example.com", wantErr: "scheme"},
		{name: "should reject a non-http scheme", publicURL: "ftp://ssh.example.com", wantErr: "scheme"},
		{name: "should reject a missing host", publicURL: "https://", wantErr: "host"},
		// Serving under a sub-path would need the frontend's base to move
		// with it, so accepting one would only yield a redirect URI that
		// silently does not work.
		{name: "should reject a path", publicURL: "https://ssh.example.com/ssoossh", wantErr: "origin only"},
		{name: "should reject a query", publicURL: "https://ssh.example.com?a=b", wantErr: "origin only"},
		{name: "should reject a fragment", publicURL: "https://ssh.example.com#frag", wantErr: "origin only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := (&HTTPSettings{PublicURL: tt.publicURL}).Validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("got error %v, want none", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("got no error, want one mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got error %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}
