//go:build e2e

package e2e

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/testutil"
	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Email notifications are the first feature whose whole output leaves the
// process, so a unit test can only ever prove the pieces. What has to be
// established end to end is the property the design turns on: an approval
// and a redemption each produce a real message, delivered over a real SMTP
// conversation to the approver's real address, naming the service account
// and linking to the code's page, but never carrying the enrollment code.
//
// The code's absence is the assertion that matters most. It is a bearer
// credential that mints certificates unattended, and mail is stored,
// forwarded, and indexed on systems the server knows nothing about.

// notifyServiceAccount is the account the enrollment under test is for.
const notifyServiceAccount = "notify-bot"

// notifyApproverEmail is the address the harness IdP releases for alice,
// and therefore the address every notification here must reach.
const notifyApproverEmail = "alice@example.org"

// notificationFixture is a server relaying mail to an in-process sink, plus
// the built client binary.
type notificationFixture struct {
	Server *harness.Server
	Sink   *testutil.SMTPSink
	Bin    string
}

func newNotificationFixture(t *testing.T) *notificationFixture {
	t.Helper()

	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})

	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		ServiceAccountsField: serviceAccountClaim,
		// A local relay with no TLS and no authentication: the shape the
		// defaults are written for, and the one an operator gets by
		// pointing ssoosshd at the mail system already on the box.
		ExtraConfigYAML: fmt.Sprintf(`mail:
  enabled: true
  from: "ssoossh <ssoossh@example.org>"
  subject_prefix: "[ssoossh] "
  smtp:
    host: %q
    port: %d
    tls: "off"
    auth: "none"
    timeout: 10s
`, sink.Host, sink.Port),
	})
	_, bin := harness.Binaries(t)

	return &notificationFixture{Server: srv, Sink: sink, Bin: bin}
}

// approve authenticates as alice — who holds the service account and has an
// email address — and approves requestID for that account.
func (f *notificationFixture) approve(t *testing.T, requestID string) {
	t.Helper()

	client := newBrowserClient(t)
	err := harness.AuthenticateWithExtraClaims(client, f.Server.BaseURL, "/approve/"+requestID, "alice", nil,
		map[string]any{
			serviceAccountClaim: []string{notifyServiceAccount},
			"email":             notifyApproverEmail,
		})
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	if err := harness.ApproveService(client, f.Server.BaseURL, requestID, notifyServiceAccount); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

// enroll runs `service enroll` through to a printed code, approving along
// the way, and returns the code and the command's own output.
func (f *notificationFixture) enroll(t *testing.T, keyPath string, extraArgs ...string) (code, stdout string) {
	t.Helper()

	args := append([]string{"service", "enroll", "--key", keyPath, "--server", f.Server.BaseURL}, extraArgs...)
	proc := harness.StartClient(t, f.Bin, harness.ClientOptions{Args: args})

	requestID := requestIDFromApprovalURL(t, proc.ApprovalURL(t, waitFor))
	f.approve(t, requestID)

	if err := proc.Wait(t, waitFor); err != nil {
		t.Fatalf("service enroll failed: %v\nstdout:\n%s\nstderr:\n%s", err, proc.Stdout(), proc.Stderr())
	}

	out := proc.Stdout()
	match := enrollmentCodePattern.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("service enroll printed no enrollment code\nstdout:\n%s", out)
	}
	return match[1], out
}

// TestServiceNotifications_ShouldEmailTheApproverWhenAnEnrollmentIsCreated
// is tier 1: real SMTP over a real socket, from the real server process.
func TestServiceNotifications_ShouldEmailTheApproverWhenAnEnrollmentIsCreated(t *testing.T) {
	f := newNotificationFixture(t)
	keyPath := filepath.Join(t.TempDir(), "notify-key")

	code, _ := f.enroll(t, keyPath)

	messages := f.Sink.WaitForMessages(t, 1, waitFor)
	msg := messages[0]

	if len(msg.To) != 1 || msg.To[0] != notifyApproverEmail {
		t.Errorf("delivered to %v, want the approver's address %q", msg.To, notifyApproverEmail)
	}
	if msg.From != "ssoossh@example.org" {
		t.Errorf("envelope sender = %q", msg.From)
	}

	subject := msg.Header("Subject")
	if !strings.Contains(subject, "[ssoossh]") {
		t.Errorf("subject %q is missing the configured prefix", subject)
	}
	if !strings.Contains(subject, notifyServiceAccount) {
		t.Errorf("subject %q does not name the service account", subject)
	}

	// The message says what happened and links to where the approver acts
	// on it. The ssh_config recipe stays in the terminal, which prints it
	// with real values. alice is neither SOC nor admin, so her first login
	// set the simplified email level: no Details section.
	body := textBody(t, msg)
	for _, want := range []string{
		notifyServiceAccount,
		f.Server.BaseURL + "/service-codes/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("message body does not mention %q", want)
		}
	}
	if strings.Contains(body, "Key ID") {
		t.Error("a simplified message carries the Details section")
	}

	// And it must not carry the one thing that never leaves the terminal.
	if strings.Contains(msg.Data, code) || strings.Contains(body, code) {
		t.Error("the enrollment code was delivered by email")
	}
}

// TestServiceNotifications_ShouldAddDetailsAtTheAdvancedEmailLevel: an
// approver who chose the advanced email level gets the Details section,
// and still never the code.
func TestServiceNotifications_ShouldAddDetailsAtTheAdvancedEmailLevel(t *testing.T) {
	f := newNotificationFixture(t)
	keyPath := filepath.Join(t.TempDir(), "notify-key")

	// Sign in first, so alice's account exists to hold the level.
	client := newBrowserClient(t)
	err := harness.AuthenticateWithExtraClaims(client, f.Server.BaseURL, "/preferences", "alice", nil,
		map[string]any{
			serviceAccountClaim: []string{notifyServiceAccount},
			"email":             notifyApproverEmail,
		})
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	if err := harness.SetEmailDetailLevel(client, f.Server.BaseURL, model.DetailLevelAdvanced); err != nil {
		t.Fatalf("harness: %v", err)
	}

	code, _ := f.enroll(t, keyPath)

	body := textBody(t, f.Sink.WaitForMessages(t, 1, waitFor)[0])
	for _, want := range []string{"DETAILS", "Key ID", "Bound key"} {
		if !strings.Contains(body, want) {
			t.Errorf("advanced message body does not mention %q", want)
		}
	}
	if strings.Contains(body, code) {
		t.Error("the enrollment code was delivered by email")
	}
}

// textBody returns the decoded text/plain part of msg. Mail goes out
// quoted-printable, which soft-wraps long lines such as a URL, so the raw
// DATA cannot be searched for one. multipart.Reader decodes
// quoted-printable on its own; base64 is decoded here.
func textBody(t *testing.T, msg testutil.SMTPMessage) string {
	t.Helper()

	m, err := mail.ReadMessage(strings.NewReader(msg.Data))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("message is %s, want multipart/alternative", mediaType)
	}

	parts := multipart.NewReader(m.Body, params["boundary"])
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			t.Fatal("message has no text/plain part")
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if !strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
			continue
		}
		var r io.Reader = part
		if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
			r = base64.NewDecoder(base64.StdEncoding, part)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("decode text part: %v", err)
		}
		return string(body)
	}
}

// TestServiceNotifications_ShouldEmailTheApproverOnEveryRedemption covers
// the second notification and, with it, that a reusable code produces a
// message per use rather than one per enrollment.
func TestServiceNotifications_ShouldEmailTheApproverOnEveryRedemption(t *testing.T) {
	f := newNotificationFixture(t)
	keyPath := filepath.Join(t.TempDir(), "notify-key")

	code, _ := f.enroll(t, keyPath)
	f.Sink.WaitForMessages(t, 1, waitFor)

	retrieve := harness.StartClient(t, f.Bin, harness.ClientOptions{Args: []string{
		"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL, "--force",
	}})
	if err := retrieve.Wait(t, waitFor); err != nil {
		t.Fatalf("service retrieve failed: %v\nstdout:\n%s\nstderr:\n%s", err, retrieve.Stdout(), retrieve.Stderr())
	}

	messages := f.Sink.WaitForMessages(t, 2, waitFor)
	redemption := messages[1]

	if len(redemption.To) != 1 || redemption.To[0] != notifyApproverEmail {
		t.Errorf("delivered to %v, want the approver's address", redemption.To)
	}
	if subject := redemption.Header("Subject"); !strings.Contains(strings.ToLower(subject), "redeemed") {
		t.Errorf("subject %q does not report a redemption", subject)
	}
	if !strings.Contains(redemption.Data, notifyServiceAccount) {
		t.Error("the redemption message does not name the service account")
	}
	if strings.Contains(redemption.Data, code) {
		t.Error("the enrollment code was delivered by email")
	}
}

// TestServiceNotifications_ShouldStopSendingWhenTheUserOptsOut is the
// preferences page's reason to exist, proved against the server rather
// than against the store behind it.
func TestServiceNotifications_ShouldStopSendingWhenTheUserOptsOut(t *testing.T) {
	f := newNotificationFixture(t)
	keyPath := filepath.Join(t.TempDir(), "notify-key")

	// One enrollment first, so the approver's user record exists to hang a
	// preference off and the default-on behavior is established.
	code, _ := f.enroll(t, keyPath)
	f.Sink.WaitForMessages(t, 1, waitFor)

	client := newBrowserClient(t)
	err := harness.AuthenticateWithExtraClaims(client, f.Server.BaseURL, "/preferences", "alice", nil,
		map[string]any{
			serviceAccountClaim: []string{notifyServiceAccount},
			"email":             notifyApproverEmail,
		})
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	if err := harness.SetNotificationPreference(client, f.Server.BaseURL, "service_enrollment_redeemed", false); err != nil {
		t.Fatalf("harness: %v", err)
	}

	retrieve := harness.StartClient(t, f.Bin, harness.ClientOptions{Args: []string{
		"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL, "--force",
	}})
	if err := retrieve.Wait(t, waitFor); err != nil {
		t.Fatalf("service retrieve failed: %v\nstdout:\n%s\nstderr:\n%s", err, retrieve.Stdout(), retrieve.Stderr())
	}

	// The certificate was issued; only the notification was suppressed.
	// Give delivery a chance to happen before concluding it did not.
	time.Sleep(2 * time.Second)
	if got := len(f.Sink.Messages()); got != 1 {
		t.Errorf("sink holds %d messages, want the redemption notification suppressed", got)
	}
}
