// Package testutil holds test helpers shared across server subpackages
// (not imported by non-test code).
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// NewTestOIDCProvider starts an httptest.Server serving a minimal OIDC
// discovery document and an empty JWKS, enough for code under test to run
// OIDC provider discovery against. It is closed via t.Cleanup.
func NewTestOIDCProvider(t *testing.T) *httptest.Server {
	t.Helper()

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// json.Encoder rather than Fprintf so the document is built by a
		// real JSON writer (and semgrep's XSS rule stays quiet).
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/auth",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"keys":[]}`)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
