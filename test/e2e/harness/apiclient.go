//go:build e2e || resilience || load

package harness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// This file is the non-browser stand-in for a person approving a request:
// an http.Client with a cookie jar that walks the server's real OIDC
// redirect chain and posts to the approval endpoints, without rendering
// HTML or running JavaScript. The e2e suite's tier 1 uses it to keep the
// wire honest; the load suite uses it because one headless Chrome per
// simulated user does not scale past a handful.
//
// Nothing here takes a *testing.T. These calls run inside worker goroutines
// under load, where t.Fatalf is not allowed, so the caller decides whether a
// failure is fatal or just one more entry in a failure count.

// NewCookieClient returns an http.Client with a cookie jar, so a session
// established by Authenticate survives into later calls.
func NewCookieClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	return &http.Client{Jar: jar}, nil
}

// RequestIDFromApprovalURL extracts the certificate request's UUID from an
// approval URL of the form "http://host/approve/<id>".
func RequestIDFromApprovalURL(approvalURL string) (string, error) {
	u, err := url.Parse(approvalURL)
	if err != nil {
		return "", fmt.Errorf("parse approval URL %q: %w", approvalURL, err)
	}
	id := strings.TrimPrefix(u.Path, "/approve/")
	if id == "" || id == u.Path {
		return "", fmt.Errorf("approval URL %q does not look like /approve/<id>", approvalURL)
	}
	return id, nil
}

// Authenticate drives client through the full OIDC login against the
// harness IdP: GET .../auth/login?return_to=..., submit the IdP's real
// login form as username (with groups, if any), and follow the resulting
// redirect chain back into the server. client's cookie jar holds an
// authenticated session afterward.
func Authenticate(client *http.Client, serverBaseURL, returnTo, username string, groups []string) error {
	return AuthenticateWithExtraClaims(client, serverBaseURL, returnTo, username, groups, nil)
}

// AuthenticateWithExtraClaims is Authenticate with additional ID token
// claims stamped by the harness IdP (see its extra_claims form field) —
// for tests exercising authentication.fields.extra.
func AuthenticateWithExtraClaims(client *http.Client, serverBaseURL, returnTo, username string, groups []string, extraClaims map[string]any) error {
	loginURL := serverBaseURL + "/auth/login?return_to=" + url.QueryEscape(returnTo)
	resp, err := client.Get(loginURL)
	if err != nil {
		return fmt.Errorf("reach the OIDC login redirect: %w", err)
	}
	resp.Body.Close()

	idpAuthorizeURL := resp.Request.URL.String()

	form := url.Values{"username": {username}}
	for _, g := range groups {
		form.Add("groups", g)
	}
	if len(extraClaims) > 0 {
		extraJSON, err := json.Marshal(extraClaims)
		if err != nil {
			return fmt.Errorf("encode extra claims: %w", err)
		}
		form.Add("extra_claims", string(extraJSON))
	}

	resp2, err := client.PostForm(idpAuthorizeURL, form)
	if err != nil {
		return fmt.Errorf("submit the IdP login form: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("expected 200 after completing OIDC login, got %d (final URL %s)",
			resp2.StatusCode, resp2.Request.URL)
	}
	return nil
}

// FetchCAKeys GETs /api/ca and parses every key in the (possibly
// multi-line) response — the registry-backed endpoint returns one
// authorized_keys-format key per active signer.
func FetchCAKeys(serverBaseURL string) ([]ssh.PublicKey, error) {
	resp, err := http.Get(serverBaseURL + "/api/ca")
	if err != nil {
		return nil, fmt.Errorf("get /api/ca: %w", err)
	}
	defer resp.Body.Close()

	var envelope apitypes.Envelope[apitypes.CAResponse]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode /api/ca response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/api/ca failed: status %d, error %q", resp.StatusCode, envelope.Error)
	}

	var keys []ssh.PublicKey
	for _, line := range strings.Split(envelope.Data.CA, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, err := ParseAuthorizedKey(line)
		if err != nil {
			return nil, fmt.Errorf("parse CA key line %q: %w", line, err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// Approve POSTs approve for requestID as whoever client is authenticated
// as. The certificate (or denial) still arrives on the waiting client's own
// SSE connection separately.
func Approve(client *http.Client, serverBaseURL, requestID string) error {
	return decide[apitypes.ApproveResponse](client, serverBaseURL, requestID, "approve")
}

// Deny POSTs deny for requestID as whoever client is authenticated as.
func Deny(client *http.Client, serverBaseURL, requestID string) error {
	return decide[apitypes.DenyResponse](client, serverBaseURL, requestID, "deny")
}

// decide posts one of the two decision endpoints and checks the envelope
// the server answers with. Both endpoints have the same shape, differing
// only in the payload type the envelope carries.
func decide[T any](client *http.Client, serverBaseURL, requestID, action string) error {
	resp, err := client.Post(serverBaseURL+"/api/certs/requests/"+requestID+"/"+action, "application/json", nil)
	if err != nil {
		return fmt.Errorf("post %s: %w", action, err)
	}
	defer resp.Body.Close()

	var envelope apitypes.Envelope[T]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s failed: status %d, error %q", action, resp.StatusCode, envelope.Error)
	}
	return nil
}
