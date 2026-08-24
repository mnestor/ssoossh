//go:build e2e

// Package e2e includes the multi-instance deployment tests: two ssoosshd
// instances sharing one postgres database and one NATS broker, the shape a
// load balancer fronts. This file was quarantined behind a
// multi_instance_test build tag while ssoosshd was single-instance only;
// the NATS pubsub backend (server/config/types_pubsub.go) is what made the
// topology real, so the quarantine is gone and CI runs these in the
// multi-signer job, which already provides docker for NATS and a postgres
// service.
package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// startClusteredPair starts two ssoosshd instances sharing one postgres
// database *and* one NATS broker. Deliberately distinct from
// multisigner_test.go's startSharedPair, which shares only the database:
// without a cross-instance broker each instance's wake notifications stay
// inside its own process, which is exactly the gap these tests cover.
func startClusteredPair(t *testing.T) (srvA, srvB *harness.Server) {
	t.Helper()

	dsn := harness.NewPostgresDatabase(t)
	// Distinct from split_test.go's 42431 and multisigner_test.go's 42433.
	nats := harness.StartNATS(t, 42435)
	idp := harness.NewIdentityProvider(t)

	opts := harness.ServerOptions{DSN: dsn, ExtraConfigYAML: nats.PubSubYAML()}
	return harness.StartServer(t, idp, opts), harness.StartServer(t, idp, opts)
}

// TestMultiInstance_ShouldDeliverACertificateWhenApprovalLandsOnAnotherInstance
// is the load-balanced case an in-process broker cannot serve: the client
// holds its event stream open on instance B while the approver's browser
// lands on instance A. A publishes the wake over NATS, B's subscriber
// decodes it, and B resolves the request its own cache never saw approved.
//
// With gochannel this ended in 410 Gone: B read status=Approved from the
// shared database, but the certificate itself lived only in A's memory and
// no wake ever crossed the process boundary.
func TestMultiInstance_ShouldDeliverACertificateWhenApprovalLandsOnAnotherInstance(t *testing.T) {
	srvA, srvB := startClusteredPair(t)
	_, ssoosshBin := harness.Binaries(t)
	agent := harness.StartAgent(t)

	// The client only ever talks to B: it creates the request there and
	// waits there.
	login := harness.StartLogin(t, ssoosshBin, srvB.BaseURL, agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	waitUntilWaiting(t, login)

	// The approver lands on A, the instance holding no waiter.
	approve(t, newBrowserClient(t), srvA.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("client waiting on instance B never got its certificate after approval on instance A: %v\nstderr:\n%s",
			err, login.Stderr())
	}

	if certs := agent.Certificates(t); len(certs) != 1 {
		t.Fatalf("got %d certificates in the agent after a cross-instance approval, want 1", len(certs))
	}
}

// waitUntilWaiting blocks until the client reports that it is waiting, which
// is when it has opened its event stream and the serving instance has
// subscribed to the request's wake topic. No settle beyond that: closing the
// window between the subscription and the approval is the point of the test.
func waitUntilWaiting(t *testing.T, cp *harness.ClientProcess) {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for !strings.Contains(cp.Stderr(), "Waiting for approval") {
		if time.Now().After(deadline) {
			t.Fatalf("client never reported that it was waiting\nstderr:\n%s", cp.Stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
