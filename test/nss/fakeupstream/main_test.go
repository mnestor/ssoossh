package main

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// should answer each lookup the way the real server would when the key is
// right, and refuse every request when it is not.
func TestHandlerShouldServeTheDirectoryWhenTheKeyIsRight(t *testing.T) {
	t.Parallel()

	h := newHandler(newDirectory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		name     string
		path     string
		id       string
		secret   string
		wantCode int
		wantErr  string
		wantName string
	}{
		{name: "should find a user by name when the key is right", path: "/api/nss/user/name/alice", id: keyID, secret: keySecret, wantCode: 200, wantName: "alice"},
		{name: "should find a user by uid when the key is right", path: "/api/nss/user/uid/60002", id: keyID, secret: keySecret, wantCode: 200, wantName: "bob"},
		{name: "should find a group by name when the key is right", path: "/api/nss/group/name/devs", id: keyID, secret: keySecret, wantCode: 200, wantName: "devs"},
		{name: "should find a group by gid when the key is right", path: "/api/nss/group/gid/60001", id: keyID, secret: keySecret, wantCode: 200, wantName: "alice"},
		{name: "should answer nss_not_found when the name is unknown", path: "/api/nss/user/name/carol", id: keyID, secret: keySecret, wantCode: 404, wantErr: apitypes.ErrorCodeNSSNotFound},
		{name: "should answer nss_not_found when the gid is unknown", path: "/api/nss/group/gid/7", id: keyID, secret: keySecret, wantCode: 404, wantErr: apitypes.ErrorCodeNSSNotFound},
		{name: "should refuse when the secret is wrong", path: "/api/nss/user/name/alice", id: keyID, secret: "wrong", wantCode: 401, wantErr: apitypes.ErrorCodeNSSUnauthorized},
		{name: "should refuse when there is no key", path: "/api/nss/user/name/alice", wantCode: 401, wantErr: apitypes.ErrorCodeNSSUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set(apitypes.HeaderNSSKeyID, tt.id)
			req.Header.Set(apitypes.HeaderNSSKeySecret, tt.secret)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			var body apitypes.Envelope[struct {
				Name string `json:"name"`
			}]
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if rec.Code != tt.wantCode || body.ErrorCode != tt.wantErr || body.Data.Name != tt.wantName {
				t.Errorf("got %d %q %q, want %d %q %q", rec.Code, body.ErrorCode, body.Data.Name, tt.wantCode, tt.wantErr, tt.wantName)
			}
		})
	}
}

// should issue a server certificate the CA verifies for every name asked
// for, the loopback addresses included.
func TestNewCertificatesShouldCoverEveryHostWhenExtraHostsAreGiven(t *testing.T) {
	t.Parallel()

	cert, caPEM, err := newCertificates([]string{"vm.example.test", "192.0.2.7"})
	if err != nil {
		t.Fatalf("newCertificates: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("ca.pem holds no certificate")
	}
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "vm.example.test", "192.0.2.7"} {
		t.Run("should verify for "+host, func(t *testing.T) {
			t.Parallel()
			if _, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool}); err != nil {
				t.Errorf("verify for %s: %v", host, err)
			}
		})
	}
}
