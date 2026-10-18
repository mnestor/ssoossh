package main

// Test methodology: the helper's pieces are tested where the e2e job
// relies on them. The key shapes and the hash are pinned against the
// server's format; the database writes run on testdb's migrated database
// and, for the subcommands' file handling, on a temporary sqlite file; the
// provider and the login run against httptest servers standing in for
// ssoosshd.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/testdb"
)

func TestRun_ShouldRefuseWhenTheSubcommandIsMissingOrUnknown(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "should print the usage when no subcommand is given", args: nil, want: "usage"},
		{name: "should name the subcommand when it is unknown", args: []string{"frobnicate"}, want: `"frobnicate"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(t.Context(), tt.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run(%v) = %v, want an error containing %s", tt.args, err, tt.want)
			}
		})
	}
}

func TestKeyShapes_ShouldMatchTheServersFormatWhenDrawn(t *testing.T) {
	id, err := newKeyID()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := newKeySecret()
	if err != nil {
		t.Fatal(err)
	}
	raw, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, keySecretPrefix))
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should be nss_ and 8 lower-case Crockford symbols when an id is drawn", ok: regexp.MustCompile(`^nss_[0-9abcdefghjkmnpqrstvwxyz]{8}$`).MatchString(id)},
		{name: "should start with the secret prefix when a secret is drawn", ok: strings.HasPrefix(secret, keySecretPrefix)},
		{name: "should carry 32 random bytes when a secret is drawn", ok: decodeErr == nil && len(raw) == 32},
		{name: "should hash to hex SHA-256 when stored", ok: hashSecret("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("id %q, secret shape wrong", id)
			}
		})
	}
}

func TestNewServerCertificate_ShouldPinTheCertificatesKeyWhenMade(t *testing.T) {
	certPEM, _, pin, err := newServerCertificate("server", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should give the SPKI digest when the pin is read", ok: pin == base64.StdEncoding.EncodeToString(sum[:])},
		{name: "should name the host when the certificate is checked", ok: cert.VerifyHostname("server") == nil},
		{name: "should name the loopback address when the certificate is checked", ok: cert.VerifyHostname("127.0.0.1") == nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Error("certificate check failed")
			}
		})
	}
}

func TestRunSetup_ShouldWriteEveryFileWithItsModeWhenRun(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run(t.Context(), []string{"setup", "-out", dir, "-host", "server"}, &out); err != nil {
		t.Fatal(err)
	}
	pin, err := os.ReadFile(filepath.Join(dir, "spki.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(pin) {
		t.Errorf("printed %q, spki.txt holds %q", out.String(), pin)
	}
	tests := []struct {
		name string
		file string
		mode os.FileMode
	}{
		{name: "should leave the certificate readable when written", file: "tls.crt", mode: 0o644},
		{name: "should keep the TLS key private when written", file: "tls.key", mode: 0o600},
		{name: "should keep the CA key private when written", file: "ca-key", mode: 0o600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(dir, tt.file))
			if err != nil || info.Mode().Perm() != tt.mode {
				t.Errorf("%s: %v, mode %v, want %v", tt.file, err, info, tt.mode)
			}
		})
	}
}

func TestRunSetup_ShouldFailWhenTheDirectoryIsMissing(t *testing.T) {
	err := run(t.Context(), []string{"setup", "-out", filepath.Join(t.TempDir(), "absent")}, &bytes.Buffer{})
	if err == nil {
		t.Error("setup into a missing directory succeeded")
	}
}

func TestSeedKey_ShouldStoreOnlyTheHashWhenAKeyIsSeeded(t *testing.T) {
	db := testdb.New(t)
	now := time.Now().UTC()
	id, secret, err := seedKey(t.Context(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := seedKey(t.Context(), db, now); err != nil {
		t.Fatalf("a second seed failed, want the creator row reused: %v", err)
	}
	var key model.NSSKey
	if err := db.Where("id = ?", id).Take(&key).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should store the secret's hash when seeded", ok: key.SecretHash == hashSecret(secret)},
		{name: "should start at generation one when seeded", ok: key.CurrentGeneration == 1},
		{name: "should name the seeded creator when seeded", ok: key.CreatedBy == creatorID},
		{name: "should expire in the future when seeded", ok: key.ExpiresAt.After(now)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("key = %+v", key)
			}
		})
	}
}

func TestRevokeKey_ShouldRevokeOnlyALiveKeyWhenAsked(t *testing.T) {
	db := testdb.New(t)
	now := time.Now().UTC()
	id, _, err := seedKey(t.Context(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "should revoke the key when it is live", id: id},
		{name: "should fail when the key is already revoked", id: id, wantErr: true},
		{name: "should fail when no key has the id", id: "nss_absent00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := revokeKey(t.Context(), db, tt.id, now); (err != nil) != tt.wantErr {
				t.Errorf("revokeKey(%s) = %v, want error %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestLinkState_ShouldReportTheLoginLinkWhenAskedByName(t *testing.T) {
	db := testdb.New(t)
	now := time.Now().UTC()
	person := model.User{ID: "u-ada", Subject: "ada", Username: "ada", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&person).Error; err != nil {
		t.Fatal(err)
	}
	seedPosix(t, db, "ada", 10001, &person.ID)
	seedPosix(t, db, "svc-backup", 10002, nil)
	tests := []struct {
		name    string
		account string
		want    string
		wantErr bool
	}{
		{name: "should say linked when a login set user_id", account: "ada", want: "linked"},
		{name: "should say unlinked when nobody logged in", account: "svc-backup", want: "unlinked"},
		{name: "should fail when the account is not stored", account: "nobody", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := linkState(t.Context(), db, tt.account)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("linkState(%s) = %q, %v; want %q, error %v", tt.account, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// seedPosix writes one posix account, linked to userID when it is set.
func seedPosix(t *testing.T, db *gorm.DB, name string, uid uint32, userID *string) {
	t.Helper()
	now := time.Now().UTC()
	row := model.PosixUser{
		ID: "p-" + name, UID: uid, GID: new(uid), Name: name, DN: "uid=" + name + ",dc=example,dc=test",
		UserID: userID, Gecos: name, CreatedAt: now, UpdatedAt: now, LastSyncedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed posix account %s: %v", name, err)
	}
}

func TestDatabaseSubcommands_ShouldWorkOnASqliteFileWhenRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssoossh.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.NSSKey{}, &model.PosixUser{}); err != nil {
		t.Fatal(err)
	}
	seedPosix(t, db, "svc-backup", 10002, nil)
	keyFile := filepath.Join(dir, "nss.key")

	var out bytes.Buffer
	if err := run(t.Context(), []string{"seed-key", "-db", path, "-key-file", keyFile}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	body, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var linked bytes.Buffer
	linkErr := run(t.Context(), []string{"linked", "-db", path, "-name", "svc-backup"}, &linked)
	revokeErr := run(t.Context(), []string{"revoke-key", "-db", path, "-id", id}, &bytes.Buffer{})

	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should print only the key id when seeding", ok: len(lines) == 2 && lines[0] == id && !strings.Contains(out.String(), lines[1])},
		{name: "should write the secret to a private key file when seeding", ok: info.Mode().Perm() == 0o600 && strings.HasPrefix(lines[1], keySecretPrefix)},
		{name: "should report the link state when asked", ok: linkErr == nil && linked.String() == "unlinked\n"},
		{name: "should revoke the seeded key when asked", ok: revokeErr == nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("out %q, lines %d, mode %v, linked %q (%v), revoke %v", out.String(), len(lines), info.Mode(), linked.String(), linkErr, revokeErr)
			}
		})
	}
}

func TestDatabaseSubcommands_ShouldFailWhenTheDatabaseIsNotNamed(t *testing.T) {
	for _, sub := range []string{"seed-key", "revoke-key", "linked"} {
		t.Run("should fail when "+sub+" has no -db", func(t *testing.T) {
			if err := run(t.Context(), []string{sub}, &bytes.Buffer{}); err == nil {
				t.Errorf("%s without -db succeeded", sub)
			}
		})
	}
}

// providerServer starts the identity provider on an httptest server.
func providerServer(t *testing.T) (*identityProvider, *httptest.Server) {
	t.Helper()
	var p *identityProvider
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.handler().ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	var err error
	if p, err = newIdentityProvider(srv.URL); err != nil {
		t.Fatal(err)
	}
	return p, srv
}

// noRedirect is a client that returns redirects instead of following them.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestIdentityProvider_ShouldSignInThePostedUserWhenTheCodeIsExchanged(t *testing.T) {
	p, srv := providerServer(t)
	client := noRedirect()

	authorize := srv.URL + "/authorize?" + url.Values{
		"redirect_uri": {"https://server:8443/auth/callback"}, "state": {"st"}, "nonce": {"nc"},
	}.Encode()
	resp, err := client.PostForm(authorize, url.Values{"username": {"ada"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	callback, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.PostForm(srv.URL+"/token", url.Values{"code": {callback.Query().Get("code")}, "client_id": {"nss-e2e"}})
	if err != nil {
		t.Fatal(err)
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tokens)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(tokens.IDToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var std jwt.Claims
	var private struct {
		Username string `json:"preferred_username"`
		Nonce    string `json:"nonce"`
	}
	verifyErr := parsed.Claims(&p.key.PublicKey, &std, &private)

	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should carry the state back when the form is posted", ok: callback.Query().Get("state") == "st"},
		{name: "should sign a token the JWKS key verifies when the code is exchanged", ok: verifyErr == nil},
		{name: "should name the posted user when the token is read", ok: std.Subject == "ada" && private.Username == "ada"},
		{name: "should echo the nonce when the token is read", ok: private.Nonce == "nc"},
		{name: "should issue as its own base when the token is read", ok: std.Issuer == srv.URL},
		{name: "should address the client when the token is read", ok: std.Audience.Contains("nss-e2e")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("callback %s, claims %+v %+v, verify %v", callback, std, private, verifyErr)
			}
		})
	}
}

func TestIdentityProvider_ShouldRefuseBadRequestsWhenServing(t *testing.T) {
	_, srv := providerServer(t)
	client := noRedirect()
	tests := []struct {
		name   string
		do     func() (*http.Response, error)
		status int
	}{
		{name: "should serve discovery when asked", status: http.StatusOK,
			do: func() (*http.Response, error) { return client.Get(srv.URL + "/.well-known/openid-configuration") }},
		{name: "should serve the JWKS when asked", status: http.StatusOK,
			do: func() (*http.Response, error) { return client.Get(srv.URL + "/jwks") }},
		{name: "should render the form when the authorization endpoint is fetched", status: http.StatusOK,
			do: func() (*http.Response, error) { return client.Get(srv.URL + "/authorize") }},
		{name: "should refuse a form post when it names no user", status: http.StatusBadRequest,
			do: func() (*http.Response, error) { return client.PostForm(srv.URL+"/authorize", url.Values{}) }},
		{name: "should refuse a code when it was never issued", status: http.StatusBadRequest,
			do: func() (*http.Response, error) {
				return client.PostForm(srv.URL+"/token", url.Values{"code": {"never"}})
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tt.do()
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.status)
			}
		})
	}
}

// fakeServer stands in for ssoosshd's login: /auth/login sends the browser
// to the provider, /auth/callback lands it on /api/users/me, which names
// the user the provider's code was for. It returns the server and its
// certificate's PEM file.
func fakeServer(t *testing.T, idp string) (*httptest.Server, string) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		target := idp + "/authorize?" + url.Values{"redirect_uri": {srv.URL + "/auth/callback"}, "state": {"st"}}.Encode()
		http.Redirect(w, r, target, http.StatusFound)
	})
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: r.URL.Query().Get("code"), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/api/users/me", http.StatusFound)
	})
	mux.HandleFunc("/api/users/me", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session"); err != nil {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"data": map[string]string{"username": "ada"}})
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "tls.crt")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, caFile
}

func TestRunLogin_ShouldPrintTheSignedInUserWhenTheFlowCompletes(t *testing.T) {
	_, idp := providerServer(t)
	srv, caFile := fakeServer(t, idp.URL)
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "should print the username when the login completes", args: []string{"-server", srv.URL, "-ca", caFile, "-user", "ada"}, want: "ada\n"},
		{name: "should fail when no user is named", args: []string{"-server", srv.URL, "-ca", caFile}, wantErr: true},
		{name: "should fail when the certificate file is missing", args: []string{"-server", srv.URL, "-ca", caFile + ".absent", "-user", "ada"}, wantErr: true},
		{name: "should fail when the server is not trusted", args: []string{"-server", idp.URL, "-ca", caFile, "-user", "ada"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.WithoutCancel(t.Context()), append([]string{"login"}, tt.args...), &out)
			if (err != nil) != tt.wantErr || out.String() != tt.want {
				t.Errorf("login %v = %q, %v; want %q, error %v", tt.args, out.String(), err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestRunOIDC_ShouldServeUntilCanceledWhenStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"oidc", "-listen", "127.0.0.1:0"}, &bytes.Buffer{}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("oidc = %v, want a clean stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("oidc did not stop when canceled")
	}
}
