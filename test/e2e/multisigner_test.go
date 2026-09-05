//go:build e2e

package e2e

// Deliberate multi-signer vetting. This file exists because accidental
// shared-database state in CI caught a real client bug on 2026-08-23 (the
// registry-backed /api/ca went multi-line and the client could not parse
// it) — and accidental coverage is not coverage. Each test here builds a
// multi-signer topology on purpose:
//
//   - Two full `serve` instances sharing one postgres database: each
//     in-process signer announces its own CA key into the shared registry
//     (the HA shape behind a load balancer).
//   - One `serve api` instance plus two standalone `sign` processes with
//     different CA keys, joined only by NATS (the split HA-signer shape) —
//     see TestMultiSigner_TwoSplitSignersServeOneAPIInstance.
//
// The postgres-backed tests skip without SSOOSSH_E2E_POSTGRES_DSN; the NATS
// test skips without docker. CI runs all of them in the multi-signer job.

import (
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// startSharedPair starts two full ssoosshd instances against one
// deliberately shared postgres database: the multi-signer HA topology where
// each instance's in-process signer announces its own CA key into the
// shared registry.
func startSharedPair(t *testing.T) (idp *harness.IdentityProvider, srvA, srvB *harness.Server) {
	t.Helper()

	dsn := harness.NewPostgresDatabase(t)
	idp = harness.NewIdentityProvider(t)
	srvA = harness.StartServer(t, idp, harness.ServerOptions{DSN: dsn})
	srvB = harness.StartServer(t, idp, harness.ServerOptions{DSN: dsn})
	return idp, srvA, srvB
}

// loginAndGetCert drives a full login → approve → certificate flow against
// baseURL with the given private agent, mapping the certificate onto
// principal, and returns the issued certificate.
func loginAndGetCert(t *testing.T, ssoosshBin, baseURL string, agent *harness.Agent, principal string) *ssh.Certificate {
	t.Helper()

	login := harness.StartLogin(t, ssoosshBin, baseURL, agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	approve(t, newBrowserClient(t), baseURL, requestID, principal, nil)
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("login against %s failed after approval: %v\nstderr:\n%s", baseURL, err, login.Stderr())
	}
	certs := agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates in the agent after login against %s, want 1", len(certs), baseURL)
	}
	return certs[0]
}

// mustParseCA parses an authorized_keys-format CA key or fails the test.
func mustParseCA(t *testing.T, s string) ssh.PublicKey {
	t.Helper()

	key, err := harness.ParseAuthorizedKey(s)
	if err != nil {
		t.Fatalf("harness: failed to parse CA public key: %v", err)
	}
	return key
}

// containsKey reports whether keys contains want.
func containsKey(keys []ssh.PublicKey, want ssh.PublicKey) bool {
	for _, k := range keys {
		if harness.SameSSHKey(k, want) {
			return true
		}
	}
	return false
}

// TestMultiSigner_RegistryServesBothInstanceKeys: with two instances
// sharing one database, /api/ca on either instance must serve exactly the
// two announced CA keys — the merged trust set an admin deploys to sshd's
// TrustedUserCAKeys or PAM's trusted-ca-file.
func TestMultiSigner_RegistryServesBothInstanceKeys(t *testing.T) {
	_, srvA, srvB := startSharedPair(t)

	keyA := mustParseCA(t, srvA.CAPublicKey)
	keyB := mustParseCA(t, srvB.CAPublicKey)

	for _, baseURL := range []string{srvA.BaseURL, srvB.BaseURL} {
		keys, err := harness.FetchCAKeys(baseURL)
		if err != nil {
			t.Fatalf("fetch CA keys from %s: %v", baseURL, err)
		}
		if len(keys) != 2 {
			t.Fatalf("got %d CA keys from %s, want 2", len(keys), baseURL)
		}
		if !containsKey(keys, keyA) || !containsKey(keys, keyB) {
			t.Errorf("CA keys from %s do not contain both instance keys", baseURL)
		}
	}
}

// TestMultiSigner_LoginSucceedsAgainstEitherInstance: the client must
// accept the two-key /api/ca response (the regression the accidental
// shared-DB coverage caught) and end up with a certificate signed by the
// instance it logged in against.
func TestMultiSigner_LoginSucceedsAgainstEitherInstance(t *testing.T) {
	_, srvA, srvB := startSharedPair(t)
	_, ssoosshBin := harness.Binaries(t)

	certA := loginAndGetCert(t, ssoosshBin, srvA.BaseURL, harness.StartAgent(t), "alice")
	if !harness.SameSSHKey(certA.SignatureKey, mustParseCA(t, srvA.CAPublicKey)) {
		t.Error("certificate from instance A is not signed by A's CA key")
	}

	certB := loginAndGetCert(t, ssoosshBin, srvB.BaseURL, harness.StartAgent(t), "alice")
	if !harness.SameSSHKey(certB.SignatureKey, mustParseCA(t, srvB.CAPublicKey)) {
		t.Error("certificate from instance B is not signed by B's CA key")
	}
}

// TestMultiSigner_SshdAcceptsCertsFromEitherSigner: one sshd trusting the
// merged CA set (what an admin deploys by fetching /api/ca), certificates
// issued by two different signer instances, both sessions must succeed —
// and a certificate from an unrelated third instance must be refused.
func TestMultiSigner_SshdAcceptsCertsFromEitherSigner(t *testing.T) {
	idp, srvA, srvB := startSharedPair(t)
	_, ssoosshBin := harness.Binaries(t)

	sshd := harness.StartSSHD(t, srvA.CAPublicKey+"\n"+srvB.CAPublicKey)

	agentA := harness.StartAgent(t)
	loginAndGetCert(t, ssoosshBin, srvA.BaseURL, agentA, sshd.Principal)
	if out, err := harness.RunSSH(t, sshd, agentA.Socket, "true"); err != nil {
		t.Fatalf("ssh with instance A's certificate failed: %v\noutput:\n%s", err, out)
	}

	agentB := harness.StartAgent(t)
	loginAndGetCert(t, ssoosshBin, srvB.BaseURL, agentB, sshd.Principal)
	if out, err := harness.RunSSH(t, sshd, agentB.Socket, "true"); err != nil {
		t.Fatalf("ssh with instance B's certificate failed: %v\noutput:\n%s", err, out)
	}

	// Third instance on its own private database: its CA is not in sshd's
	// trusted set, so its certificate must be refused — the trust boundary
	// still holds with multiple CAs in the file.
	srvC := harness.StartServer(t, idp, harness.ServerOptions{})
	agentC := harness.StartAgent(t)
	loginAndGetCert(t, ssoosshBin, srvC.BaseURL, agentC, sshd.Principal)
	if out, err := harness.RunSSH(t, sshd, agentC.Socket, "true"); err == nil {
		t.Fatalf("ssh with an untrusted instance's certificate succeeded, want refusal\noutput:\n%s", out)
	}
}

// TestMultiSigner_TwoSplitSignersServeOneAPIInstance: the real HA signer
// topology — one `serve api` web tier, two `sign` processes holding
// different CA keys, joined only by NATS. Both signers announce into the
// registry; /api/ca must list both keys; login must yield a certificate
// signed by one of them and accepted by the client.
func TestMultiSigner_TwoSplitSignersServeOneAPIInstance(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	nats := harness.StartNATS(t)

	srv := harness.StartServer(t, idp, harness.ServerOptions{
		Args:            []string{"serve", "api"},
		ExtraConfigYAML: nats.PubSubYAML(),
	})

	ssoosshdPath, ssoosshBin := harness.Binaries(t)
	harness.StartSigner(t, ssoosshdPath, srv.ConfigPath)

	cfg2, caPub2 := harness.NewSignerConfig(t, idp, harness.ServerOptions{
		ExtraConfigYAML: nats.PubSubYAML(),
	})
	harness.StartSigner(t, ssoosshdPath, cfg2)

	keyA := mustParseCA(t, srv.CAPublicKey)
	keyB := mustParseCA(t, caPub2)

	// Both signers announce on startup; poll until the registry serves both
	// keys (announcement rides NATS, so arrival is asynchronous).
	deadline := time.Now().Add(15 * time.Second)
	for {
		keys, err := harness.FetchCAKeys(srv.BaseURL)
		if err == nil && containsKey(keys, keyA) && containsKey(keys, keyB) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry never served both signer keys: last result %v, err %v", keys, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	cert := loginAndGetCert(t, ssoosshBin, srv.BaseURL, harness.StartAgent(t), "alice")
	if !harness.SameSSHKey(cert.SignatureKey, keyA) && !harness.SameSSHKey(cert.SignatureKey, keyB) {
		t.Error("issued certificate is signed by neither registered signer key")
	}
}
