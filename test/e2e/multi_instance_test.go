//go:build e2e && multi_instance_test

// Package e2e includes tests for multi-instance deployments. This file is
// quarantined behind the multi_instance_test build tag because ssoosshd is
// currently single-instance only and cannot sit behind a load balancer.
// These tests document known limitations and will become part of the
// merge-gate suite once docs/dev/multi-instance-safety-plan.md is acted on.
//
// Run with: go test -tags=e2e,multi_instance_test ./test/e2e
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestMultiInstance_ColdCacheReturns410OnApprovedRequest reproduces the
// multi-instance defect documented in docs/dev/multi-instance-safety-plan.md:
//
// When a certificate request is approved on instance A, the approval writes
// status=Approved to the shared Postgres database and publishes a wake to
// the in-process gochannel pubsub. Instance B, using the same in-process
// broker, never receives the wake because gochannel only routes messages
// within a single process. Instance B's resolved cache remains cold.
//
// When a client calls Wait on instance B, it reads status=Approved from the
// database, but since the actual certificate is never persisted (it lives
// only in the approving instance's memory), tryHandleWakeMessage cannot
// recover it. Instance B correctly returns HTTP 410 Gone per the comment at
// certrequest.go:1043: the process restarted (or in this case, a different
// instance) and the certificate is genuinely lost.
//
// The cross-instance delivery logic in tryHandleWakeMessage (certrequest.go:1104)
// is already NATS-ready and can decode wake payloads that arrive from another
// instance. The gap is entirely that no cross-instance broker is wired: the
// server is hardcoded to use gochannel (in-process only). The fix requires
// configuring a real broker (NATS or equivalent) as documented in
// docs/dev/multi-instance-safety-plan.md.
//
// This test verifies the defect exists in the current code by:
// 1. Starting two ssoosshd instances against one shared Postgres
// 2. Creating and approving a request on instance A
// 3. Calling Wait from instance B
// 4. Asserting that B receives HTTP 410 Gone instead of the certificate
func TestMultiInstance_ColdCacheReturns410OnApprovedRequest(t *testing.T) {
	// This test requires Postgres for sharing state across instances.
	harness.StartPostgres(t)

	// Set up the shared infrastructure: IdP and two agent instances.
	idp := harness.NewIdentityProvider(t)
	// One agent, not two: only instance A ever runs a client. Step 3 below
	// reaches instance B with a direct HTTP GET rather than a second CLI
	// invocation, because the point is B's cold cache, not B's client. A
	// second agent was started here and never used, which stopped this file
	// compiling at all -- so the suite failed at `go build` rather than at
	// its assertion, and documented nothing.
	agent1 := harness.StartAgent(t)
	_, ssoosshBin := harness.Binaries(t)

	// Start instance A with Postgres backend. The environment variable
	// set by StartPostgres is picked up by StartServer via the config.
	serverA := harness.StartServer(t, idp, harness.ServerOptions{})
	defer func() {
		if serverA != nil {
			// Explicit cleanup: the fixture is torn down implicitly via t.Cleanup
			// in StartServer, but be explicit about the intent.
		}
	}()

	// Start instance B with the same database. Both instances share the
	// SSOOSSH_E2E_POSTGRES_DSN environment variable set by StartPostgres.
	serverB := harness.StartServer(t, idp, harness.ServerOptions{})
	defer func() {
		if serverB != nil {
			// Explicit cleanup
		}
	}()

	// Authenticate a browser client as alice with approver powers.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("test setup failed: could not create cookie jar: %v", err)
	}
	browser := &http.Client{Jar: jar}

	// Step 1: Create a certificate request on instance A.
	login := harness.StartLogin(t, ssoosshBin, serverA.BaseURL, agent1.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)
	requestID := requestIDFromApprovalURL(t, approvalURL)

	t.Logf("Created request %s on instance A", requestID)

	// Step 2: Approve the request on instance A.
	// This populates instance A's resolved cache and writes status=Approved
	// to the shared Postgres database, but the approval notification is only
	// published to the in-process gochannel pubsub — it never reaches
	// instance B.
	approve(t, browser, serverA.BaseURL, requestID, "alice", nil)
	t.Logf("Approved request %s on instance A", requestID)

	// Step 3: Try to Wait on the same request from instance B.
	// Instance B has a cold cache (no entry in resolved for this requestID).
	// It will read status=Approved from the database, but since the actual
	// certificate is never persisted, the code returns 410 Gone.
	//
	// Fetch the wait endpoint from instance B, which is where the client
	// would normally call Wait via SSE. We use a direct HTTP GET to the
	// internal wait endpoint instead of the CLI for direct control.
	waitResp, err := http.Get(fmt.Sprintf("%s/api/certs/requests/%s/wait", serverB.BaseURL, requestID))
	if err != nil {
		t.Fatalf("test failed: could not call Wait on instance B: %v", err)
	}
	defer waitResp.Body.Close()

	// Step 4: Assert that instance B returns 410 Gone.
	// This is the defect: the client gets no certificate, and there is no
	// way to recover it except by creating a new request and re-approving.
	if waitResp.StatusCode != http.StatusGone {
		t.Logf("instance B response status: %d (expected 410)", waitResp.StatusCode)
		t.Logf("response body: %s", waitResp.Header)

		var body apitypes.Envelope[any]
		if err := json.NewDecoder(waitResp.Body).Decode(&body); err == nil {
			t.Logf("decoded error: %v", body)
		}

		t.Fatalf("DEFECT REPRODUCED INCORRECTLY: instance B did not return 410 Gone. "+
			"The multi-instance defect is not present in the current code, or the test setup is wrong.\n"+
			"Got status %d instead.\n"+
			"This indicates the defect has been fixed or the test needs adjustment.",
			waitResp.StatusCode)
	}

	// The defect has been successfully reproduced: instance B returned 410
	// instead of the certificate because its cache was cold and the approval
	// notification never reached it via the in-process gochannel pubsub.
	t.Logf("DEFECT REPRODUCED: instance B correctly returned 410 Gone " +
		"(the certificate was never persisted, and the wake message never reached B's cache)")
}
