package config

// Test methodology: table-driven against HTTPSettings values directly, no
// viper and no file loading — these are pure functions over configuration,
// and the thing worth pinning is what a given combination of settings
// resolves to.
//
// PublicOrigin and IsTLS decide the OIDC redirect URI and the session
// cookie's Secure attribute. Both are things a deployment gets wrong once,
// silently, and then spends an afternoon on.

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
		// public_url set: it is the answer, whatever the listen config says.
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
		// while this process listens on plain HTTP somewhere else. Inference
		// would answer http://ssh.example.com:8080 and break OIDC login.
		{
			name: "should ignore the listen port when public_url is set",
			http: HTTPSettings{PublicURL: "https://ssh.example.com", ServerName: "ssh.example.com", Port: 8080},
			want: "https://ssh.example.com",
		},
		{
			name: "should ignore is_https when public_url disagrees with it",
			http: HTTPSettings{PublicURL: "http://ssh.internal", IsHTTPS: true},
			want: "http://ssh.internal",
		},

		// public_url unset: fall back to inferring from the listen config,
		// which is correct only when nothing sits in front.
		{
			name: "should omit the default https port when inferring",
			http: HTTPSettings{ServerName: "ssh.example.com", Port: 443, IsHTTPS: true},
			want: "https://ssh.example.com",
		},
		{
			name: "should omit the default http port when inferring",
			http: HTTPSettings{ServerName: "ssh.example.com", Port: 80},
			want: "http://ssh.example.com",
		},
		{
			name: "should include a non-default port when inferring",
			http: HTTPSettings{ServerName: "ssh.example.com", Port: 8080},
			want: "http://ssh.example.com:8080",
		},
		{
			name: "should include a non-default tls port when inferring",
			http: HTTPSettings{ServerName: "ssh.example.com", Port: 8443, IsHTTPS: true},
			want: "https://ssh.example.com:8443",
		},
		{
			name: "should omit the port when it is zero",
			http: HTTPSettings{ServerName: "ssh.example.com"},
			want: "http://ssh.example.com",
		},

		// Nothing to go on. Callers read "" as "unknown" rather than guessing.
		{
			name: "should return empty when neither public_url nor server_name is set",
			http: HTTPSettings{Port: 8080},
			want: "",
		},
		// An unparseable value is rejected at startup by Validate; if one
		// reaches here anyway, falling back beats returning a broken origin.
		{
			name: "should fall back to inference when public_url is invalid",
			http: HTTPSettings{PublicURL: "ssh.example.com", ServerName: "ssh.example.com", Port: 80},
			want: "http://ssh.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.http.PublicOrigin(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTLSShouldReportTheBrowserVisibleScheme(t *testing.T) {
	t.Parallel()

	// A syntactically valid keypair is not needed — HasKeyPair only checks
	// that both halves are present.
	withKeyPair := tlsutils.TLSConfig{CertificateInfo: tlsutils.CertificateInfo{Certificate: "cert", PrivateKey: "key"}}

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
		{name: "should report true when is_https is set", http: HTTPSettings{IsHTTPS: true}, want: true},
		{name: "should report false with nothing configured", http: HTTPSettings{}, want: false},
	}

	for _, tt := range tests {
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
