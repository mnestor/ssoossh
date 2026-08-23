//go:build e2e

package e2e

// The split-topology tier: `ssoosshd serve api` and `ssoosshd sign` as two
// real OS processes joined only by NATS, driven through the same login →
// approve → certificate flow as tier 1. This is the test that would have
// caught both split-mode launch bugs found by hand on 2026-08-23: the
// duplicated router runner that killed sign mode at startup, and the
// silently-discarded startup error that hid it.
//
// Requires Docker for the broker; skips cleanly when the daemon is absent,
// same as the postgres harness.

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

func TestSplitMode_CertificateIsIssuedAcrossProcesses(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	nats := harness.StartNATS(t, 42431)

	// API process: web tier + listener, no signer. StartServer waits for
	// /healthz, which also proves api mode boots against the broker.
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		Args:            []string{"serve", "api"},
		ExtraConfigYAML: nats.PubSubYAML(),
	})

	// Signer process: same config file, so both halves agree on the broker
	// and the CA key. Waits for the sign-queue subscription - core NATS
	// drops anything published before a subscriber exists.
	ssoosshdPath, ssoosshBin := harness.Binaries(t)
	harness.StartSigner(t, ssoosshdPath, srv.ConfigPath)

	agent := harness.StartAgent(t)

	login := harness.StartLogin(t, ssoosshBin, srv.BaseURL, agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)
	requestID := requestIDFromApprovalURL(t, approvalURL)

	client := newBrowserClient(t)
	approve(t, client, srv.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("split-mode login failed after approval: %v\nclient stderr:\n%s", err, login.Stderr())
	}

	certs := agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates in the agent, want 1 issued across processes", len(certs))
	}
	if got := certs[0].ValidPrincipals; len(got) != 1 || got[0] != "alice" {
		t.Errorf("got principals %v, want [alice]", got)
	}

	// The certificate must be signed by the CA key only the signer process
	// holds - the proof the signature crossed the broker rather than being
	// produced in-process.
	caKey, err := harness.ParseAuthorizedKey(srv.CAPublicKey)
	if err != nil {
		t.Fatalf("harness: failed to parse the test CA public key: %v", err)
	}
	if !harness.SameSSHKey(certs[0].SignatureKey, caKey) {
		t.Error("issued certificate is not signed by the configured test CA")
	}

}
