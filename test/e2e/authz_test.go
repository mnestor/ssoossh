//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 1 ("wire"): who may approve, deny and reach the admin API, driven
// over HTTP against the real binaries. Each test starts one server and
// signs in as many identities as it needs against it.

// The extra groups the authorization tests configure, beside the
// package-wide admin and auditor groups in fixture_test.go.
const (
	e2eSOCGroup  = "ssoossh-soc"
	e2eUserGroup = "ssoossh-users"
)

// unknownEnrollmentID names no enrollment. The SOC probe expires it: a
// caller the SOC gate lets through gets 404, one it stops gets 403, and
// nothing is changed either way.
const unknownEnrollmentID = "00000000-0000-4000-8000-000000000000"

// apiCall sends one request as whoever client is signed in as and returns
// the status. It looks like the web UI's own fetch: a JSON body when there
// is one, and Sec-Fetch-Site: same-origin, which the CSRF middleware reads
// first, so a refusal here is the authorization layer speaking rather than
// the CSRF guard.
func apiCall(t *testing.T, client *http.Client, method, target, body string) int {
	t.Helper()

	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, target, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// signedInClient returns a cookie client holding a session for username
// with groups, signed in through the harness IdP's real OIDC flow.
func signedInClient(t *testing.T, serverBaseURL, username string, groups []string) *http.Client {
	t.Helper()

	client := newBrowserClient(t)
	authenticate(t, client, serverBaseURL, "/", username, groups)
	return client
}

// assertStillWaiting fails the test if the login has already exited: a
// refused or anonymous decision must leave the request pending, so the
// client is still blocked on its event stream.
func assertStillWaiting(t *testing.T, login *harness.ClientProcess) {
	t.Helper()

	select {
	case <-login.Done():
		t.Fatalf("ssh login exited although nothing resolved its request\nstderr:\n%s", login.Stderr())
	default:
	}
}

// assertTerminalEvent reads the request's event stream and checks the
// terminal status the server reports for it.
func assertTerminalEvent(t *testing.T, serverBaseURL, requestID, want string) {
	t.Helper()

	got, err := harness.TerminalEvent(serverBaseURL, requestID, waitFor)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	if got != want {
		t.Errorf("got terminal event %q for the request, want %q", got, want)
	}
}

// TestApprove_AdminCanAccessAdminRoutes checks each role against one route
// from each tier of the admin API: admin reaches all three, SOC and
// auditor reach exactly what their role allows, a plain user gets 403 and
// a caller with no session 401. Roles come from the groups the server is
// configured with, never from anything stored.
func TestApprove_AdminCanAccessAdminRoutes(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		AdminRequireGroup: e2eAdminGroup,
		SOCGroup:          e2eSOCGroup,
		AuditorGroup:      e2eAuditorGroup,
	})

	clients := map[string]*http.Client{
		"admin":     signedInClient(t, srv.BaseURL, "ada", []string{e2eAdminGroup}),
		"soc":       signedInClient(t, srv.BaseURL, "sam", []string{e2eSOCGroup}),
		"auditor":   signedInClient(t, srv.BaseURL, "aud", []string{e2eAuditorGroup}),
		"user":      signedInClient(t, srv.BaseURL, "bob", []string{"engineering"}),
		"anonymous": newBrowserClient(t),
	}

	// One route per tier. The SOC route carries a reason because expiring
	// requires one; without it a caller past the gate would get 400 and
	// the row would no longer show which side of the gate it landed on.
	type route struct{ method, path, body string }
	auditorRoute := route{http.MethodGet, "/api/admin/config", ""}
	socRoute := route{http.MethodPatch, "/api/admin/enrollments/" + unknownEnrollmentID + "/expire", `{"reason":"e2e authorization probe"}`}
	adminRoute := route{http.MethodPost, "/api/admin/identity/echo/start", ""}

	tests := []struct {
		name  string
		role  string
		route route
		want  int
	}{
		{name: "should let an admin read the effective config", role: "admin", route: auditorRoute, want: http.StatusOK},
		{name: "should let an admin past the SOC gate", role: "admin", route: socRoute, want: http.StatusNotFound},
		{name: "should let an admin start identity echo", role: "admin", route: adminRoute, want: http.StatusOK},

		{name: "should let SOC read the effective config", role: "soc", route: auditorRoute, want: http.StatusOK},
		{name: "should let SOC past the SOC gate", role: "soc", route: socRoute, want: http.StatusNotFound},
		{name: "should refuse SOC identity echo", role: "soc", route: adminRoute, want: http.StatusForbidden},

		{name: "should let an auditor read the effective config", role: "auditor", route: auditorRoute, want: http.StatusOK},
		{name: "should refuse an auditor at the SOC gate", role: "auditor", route: socRoute, want: http.StatusForbidden},
		{name: "should refuse an auditor identity echo", role: "auditor", route: adminRoute, want: http.StatusForbidden},

		{name: "should refuse a plain user the effective config", role: "user", route: auditorRoute, want: http.StatusForbidden},
		{name: "should refuse a plain user at the SOC gate", role: "user", route: socRoute, want: http.StatusForbidden},
		{name: "should refuse a plain user identity echo", role: "user", route: adminRoute, want: http.StatusForbidden},

		{name: "should ask a caller with no session to sign in for the config", role: "anonymous", route: auditorRoute, want: http.StatusUnauthorized},
		{name: "should ask a caller with no session to sign in at the SOC gate", role: "anonymous", route: socRoute, want: http.StatusUnauthorized},
		{name: "should ask a caller with no session to sign in for identity echo", role: "anonymous", route: adminRoute, want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apiCall(t, clients[tt.role], tt.route.method, srv.BaseURL+tt.route.path, tt.route.body)
			if got != tt.want {
				t.Errorf("%s %s as %s: got status %d, want %d", tt.route.method, tt.route.path, tt.role, got, tt.want)
			}
		})
	}
}

// TestApprove_UnauthenticatedUserDeniedApproval checks that neither
// decision endpoint acts for a caller with no session, and that the
// attempt leaves the request untouched: still pending, still unclaimed,
// so its owner can approve it afterwards and receive the certificate.
func TestApprove_UnauthenticatedUserDeniedApproval(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	anonymous := newBrowserClient(t)

	tests := []struct {
		name   string
		action string
	}{
		{name: "should answer 401 when approving with no session", action: "approve"},
		{name: "should answer 401 when denying with no session", action: "deny"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := f.Server.BaseURL + "/api/certs/requests/" + requestID + "/" + tt.action
			if got := apiCall(t, anonymous, http.MethodPost, endpoint, ""); got != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", got, http.StatusUnauthorized)
			}
		})
	}

	assertStillWaiting(t, login)
	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Fatalf("got %d certificates loaded after anonymous decisions, want 0", len(certs))
	}

	approve(t, newBrowserClient(t), f.Server.BaseURL, requestID, "alice", nil)
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after its owner approved: %v\nstderr:\n%s", err, login.Stderr())
	}
	if certs := f.Agent.Certificates(t); len(certs) != 1 {
		t.Errorf("got %d certificates loaded after the owner's approval, want 1", len(certs))
	}
}

// TestApprove_EmptyGroupNeverAuthorizes runs against a server whose user
// certificates require a group and whose admin, SOC and auditor groups are
// all unset. An identity with no groups, or with a group whose name is the
// empty string, is refused the approval with 403 and leaves the request
// pending; it never matches the unset admin group either. The member row
// is the control that shows the gate is not simply refusing everyone.
func TestApprove_EmptyGroupNeverAuthorizes(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{UserRequireGroup: e2eUserGroup})
	_, ssoosshBin := harness.Binaries(t)

	tests := []struct {
		name        string
		groups      []string
		wantApprove int
	}{
		{name: "should refuse approval when the identity carries no groups", groups: nil, wantApprove: http.StatusForbidden},
		{name: "should refuse approval when the identity carries an empty group name", groups: []string{""}, wantApprove: http.StatusForbidden},
		{name: "should approve when the identity carries the required group", groups: []string{e2eUserGroup}, wantApprove: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := harness.StartAgent(t)
			login := harness.StartLogin(t, ssoosshBin, srv.BaseURL, agent.Socket)
			requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

			client := newBrowserClient(t)
			authenticate(t, client, srv.BaseURL, "/approve/"+requestID, "eve", tt.groups)

			got := apiCall(t, client, http.MethodPost, srv.BaseURL+"/api/certs/requests/"+requestID+"/approve", "")
			if got != tt.wantApprove {
				t.Fatalf("approve: got status %d, want %d", got, tt.wantApprove)
			}

			// No admin group is configured, so no identity is an admin,
			// whatever groups it carries.
			if got := apiCall(t, client, http.MethodGet, srv.BaseURL+"/api/admin/config", ""); got != http.StatusForbidden {
				t.Errorf("admin config: got status %d, want %d", got, http.StatusForbidden)
			}

			if tt.wantApprove != http.StatusOK {
				assertStillWaiting(t, login)
				if certs := agent.Certificates(t); len(certs) != 0 {
					t.Errorf("got %d certificates loaded after a refused approval, want 0", len(certs))
				}
				return
			}

			if err := login.Wait(t, waitFor); err != nil {
				t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
			}
			if certs := agent.Certificates(t); len(certs) != 1 {
				t.Errorf("got %d certificates loaded after approval, want 1", len(certs))
			}
		})
	}
}

// TestApprove_RequestDenialBlocksCertificate denies a request through the
// API and checks the whole outcome: the event stream reports a denial,
// the client exits with an error and loads nothing, and a later approval
// or second denial of the same request, even by the person who denied it,
// is refused with 409 Conflict and issues nothing.
func TestApprove_RequestDenialBlocksCertificate(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	deny(t, client, f.Server.BaseURL, requestID, "alice")

	if code := login.ExitCode(t, waitFor); code != 1 {
		t.Errorf("got exit code %d from ssh login after a denial, want 1\nstderr:\n%s", code, login.Stderr())
	}
	if !strings.Contains(login.Stderr(), "denied") {
		t.Errorf("expected stderr to report the denial, got:\n%s", login.Stderr())
	}
	assertTerminalEvent(t, f.Server.BaseURL, requestID, "denied")

	got := apiCall(t, client, http.MethodPost, f.Server.BaseURL+"/api/certs/requests/"+requestID+"/approve", "")
	if got != http.StatusConflict {
		t.Errorf("approving a denied request answered %d, want %d", got, http.StatusConflict)
	}
	got = apiCall(t, client, http.MethodPost, f.Server.BaseURL+"/api/certs/requests/"+requestID+"/deny", "")
	if got != http.StatusConflict {
		t.Errorf("denying a denied request again answered %d, want %d", got, http.StatusConflict)
	}
	assertTerminalEvent(t, f.Server.BaseURL, requestID, "denied")

	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Errorf("got %d certificates loaded after a denial, want 0", len(certs))
	}
}

// TestApprove_ExpiredRequestReturnsTimeout leaves a request unanswered on
// a server with a three-second client budget (an approval window of 2.4s)
// and checks the expiry end to end: the event stream reports "expired",
// the client exits on its own with an error saying so and loads nothing,
// and approving or denying afterwards is refused with 410 Gone and issues
// nothing.
func TestApprove_ExpiredRequestReturnsTimeout(t *testing.T) {
	f := newFixture(t, func(o *harness.ServerOptions) { o.ClientTimeout = "3s" })

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	assertStillWaiting(t, login)

	if code := login.ExitCode(t, waitFor); code != 1 {
		t.Errorf("got exit code %d from ssh login after expiry, want 1\nstderr:\n%s", code, login.Stderr())
	}
	if !strings.Contains(login.Stderr(), "expired") {
		t.Errorf("expected stderr to report the expiry, got:\n%s", login.Stderr())
	}
	assertTerminalEvent(t, f.Server.BaseURL, requestID, "expired")

	client := newBrowserClient(t)
	authenticate(t, client, f.Server.BaseURL, "/approve/"+requestID, "alice", nil)
	got := apiCall(t, client, http.MethodPost, f.Server.BaseURL+"/api/certs/requests/"+requestID+"/approve", "")
	if got != http.StatusGone {
		t.Errorf("approving an expired request answered %d, want %d", got, http.StatusGone)
	}
	got = apiCall(t, client, http.MethodPost, f.Server.BaseURL+"/api/certs/requests/"+requestID+"/deny", "")
	if got != http.StatusGone {
		t.Errorf("denying an expired request answered %d, want %d", got, http.StatusGone)
	}
	assertTerminalEvent(t, f.Server.BaseURL, requestID, "expired")

	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Errorf("got %d certificates loaded after expiry, want 0", len(certs))
	}
}

// TestApprove_AuthzErrorsAreLogged checks that every refusal reaches the
// server's log as a refused request: an access-log record at WARN naming
// the method, path and the 401 or 403 the caller got, the level an
// operator's log pipeline treats as a client error. A refused approval in
// particular must not be logged as an internal failure (ERROR, 500), and
// no record may carry the caller's session cookie.
func TestApprove_AuthzErrorsAreLogged(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		AdminRequireGroup: e2eAdminGroup,
		UserRequireGroup:  e2eUserGroup,
	})
	agent := harness.StartAgent(t)
	_, ssoosshBin := harness.Binaries(t)

	login := harness.StartLogin(t, ssoosshBin, srv.BaseURL, agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	// bob is signed in but neither an admin nor in the group user
	// certificates require, so both of his calls below are refused.
	bob := newBrowserClient(t)
	authenticate(t, bob, srv.BaseURL, "/approve/"+requestID, "bob", []string{"engineering"})

	tests := []struct {
		name   string
		client *http.Client
		method string
		path   string
		want   int
	}{
		{name: "should log a 403 when a plain user calls an admin route", client: bob, method: http.MethodGet, path: "/api/admin/config", want: http.StatusForbidden},
		{name: "should log a 403 when a non-member approves", client: bob, method: http.MethodPost, path: "/api/certs/requests/" + requestID + "/approve", want: http.StatusForbidden},
		{name: "should log a 401 when a caller with no session calls an admin route", client: newBrowserClient(t), method: http.MethodGet, path: "/api/admin/users", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apiCall(t, tt.client, tt.method, srv.BaseURL+tt.path, ""); got != tt.want {
				t.Fatalf("got status %d, want %d", got, tt.want)
			}

			record := awaitAccessLogRecord(t, srv, tt.method, tt.path)
			if want := fmt.Sprintf("response.status=%d ", tt.want); !strings.Contains(record, want) {
				t.Errorf("access-log record does not carry %q:\n%s", strings.TrimSpace(want), record)
			}
			if !strings.Contains(record, " WRN ") {
				t.Errorf("access-log record is not at WARN:\n%s", record)
			}
		})
	}

	logs := srv.Logs()
	for _, cookie := range bob.Jar.Cookies(mustParseURL(t, srv.BaseURL)) {
		if strings.Contains(logs, cookie.Value) {
			t.Errorf("the server log carries the value of session cookie %q", cookie.Name)
		}
	}
}

// awaitAccessLogRecord returns the server's access-log line for method and
// path, waiting for it because the access log is written after the
// response, so the caller can see the status before the line exists.
func awaitAccessLogRecord(t *testing.T, srv *harness.Server, method, path string) string {
	t.Helper()

	deadline := time.Now().Add(waitFor)
	for {
		for _, line := range strings.Split(srv.Logs(), "\n") {
			if strings.Contains(line, "type=accesslog") &&
				strings.Contains(line, "request.method="+method+" ") &&
				strings.Contains(line, "request.path="+path+" ") {
				return line
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no access-log record for %s %s within %s", method, path, waitFor)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mustParseURL parses raw or fails the test.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}
