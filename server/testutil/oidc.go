// Package testutil holds test helpers shared across server subpackages
// (not imported by non-test code).
package testutil

import (
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
		fmt.Fprintf(w, `{
			"issuer": %q,
			"authorization_endpoint": %q,
			"token_endpoint": %q,
			"jwks_uri": %q
		}`, srv.URL, srv.URL+"/auth", srv.URL+"/token", srv.URL+"/keys")
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"keys":[]}`)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
