package service

// Test methodology: unit tests over the "was this you?" pair emitted from
// the signed-reply path, with a capturing notifier in place of a broker.
//
// The properties that matter are which certificate types produce a message
// and which kind each produces, because the wrong answer here is either
// silence about a credential a user did not ask for or a message per
// redemption addressed to the wrong person.

import (
	"context"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// issuedReplyFixture wires a signed-reply handler with a capturing notifier
// and returns a user-owned request in Signing, ready for a reply.
func issuedReplyFixture(t *testing.T, certType model.CertificateType) (*SignedReplyHandler, *capturingNotifier, *CertRequestService, string, string) {
	t.Helper()

	svc := newTestCertRequestService(t, time.Hour)
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)
	h := newTestSignedReplyHandler(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	params := NewCertRequestParams{
		Type:      certType,
		PublicKey: "ssh-ed25519 AAAA test",
		SourceIP:  "198.51.100.7",
	}
	// The client-reported context lives in different columns per type
	// (see reportedContext): a user request carries the OS user and host
	// the client ran on, a PAM or console request the account being
	// authenticated and the machine it is on.
	switch certType {
	case model.CertificateTypePAM, model.CertificateTypeConsole:
		params.Username, params.Hostname = "alice", "workstation"
	default:
		params.LocalUsername, params.LocalHostname = "alice", "workstation"
	}
	requestID, err := svc.createRequestID(context.Background(), params)
	if err != nil {
		t.Fatalf("failed to create the %s request: %v", certType, err)
	}
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("user_id", userID).Error; err != nil {
		t.Fatalf("failed to bind the request to a user: %v", err)
	}

	return h, notifier, svc, requestID, userID
}

// successfulReply is a signed reply for requestID, filled in the way the
// signer fills one.
func successfulReply(requestID string, certType model.CertificateType) certmsg.SignedReply {
	return certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 certType,
		Certificate:          "ssh-ed25519-cert-v01@openssh.com AAAA test",
		Serial:               42,
		KeyID:                "alice",
		Principals:           []string{"alice"},
		PublicKeyFingerprint: "SHA256:test",
		Extensions:           []string{"permit-pty"},
		CriticalOptions:      map[string]string{"source-address": "198.51.100.0/24,203.0.113.0/24"},
		ValidAfter:           time.Now(),
		ValidBefore:          time.Now().Add(time.Hour),
	}
}

// An interactive certificate reaches its owner under the user kind, and the
// message carries what makes it recognizable: where the request came from,
// and what the certificate can do.
func TestResolveSuccess_shouldNotifyTheOwnerOfAUserCertificate(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, userID := issuedReplyFixture(t, model.CertificateTypeUser)

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypeUser)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	got := notifier.only(t, notify.KindUserCertificateIssued)
	if got.UserID != userID {
		t.Errorf("notified %q, want the certificate's owner %q", got.UserID, userID)
	}

	payload, ok := got.Payload.(*notify.CertificateIssued)
	if !ok {
		t.Fatalf("payload is %T, want *notify.CertificateIssued", got.Payload)
	}
	if payload.SourceIP != "198.51.100.7" {
		t.Errorf("SourceIP = %q, want the address the request was made from", payload.SourceIP)
	}
}

// Two kinds rather than one with a type field, so a user who runs sudo forty
// times a day and logs in twice can keep the login signal alone.
func TestResolveSuccess_shouldNotifyUnderThePAMKindForAPAMCertificate(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypePAM)

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypePAM)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	notifier.only(t, notify.KindPAMCertificateIssued)
}

// A console login is the type where "was this you?" matters most: the
// request came from an unauthenticated machine and was approved by a code
// typed into the owner's web session. It gets its own kind, like PAM, so
// its tolerance can be set apart from logins and sudos.
func TestResolveSuccess_shouldNotifyUnderTheConsoleKindForAConsoleCertificate(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypeConsole)

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypeConsole)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	notifier.only(t, notify.KindConsoleCertificateIssued)
}

// The "reported" line in the message reads whichever columns the request
// type stores its client-reported context in. A PAM or console request
// never sets local_username/local_hostname, so reading only those would
// leave the line blank for exactly the two types whose messages lean on it.
func TestResolveSuccess_shouldCarryTheReportedAccountAndMachineForEveryType(t *testing.T) {
	t.Parallel()

	for _, certType := range []model.CertificateType{
		model.CertificateTypeUser,
		model.CertificateTypePAM,
		model.CertificateTypeConsole,
	} {
		t.Run(string(certType), func(t *testing.T) {
			t.Parallel()

			h, notifier, _, requestID, _ := issuedReplyFixture(t, certType)

			if err := h.resolveSuccess(context.Background(), successfulReply(requestID, certType)); err != nil {
				t.Fatalf("resolveSuccess: %v", err)
			}

			got := notifier.captured()
			if len(got) != 1 {
				t.Fatalf("captured %d notifications, want 1", len(got))
			}
			payload, ok := got[0].Payload.(*notify.CertificateIssued)
			if !ok {
				t.Fatalf("payload is %T, want *notify.CertificateIssued", got[0].Payload)
			}
			if payload.LocalUsername != "alice" || payload.LocalHostname != "workstation" {
				t.Errorf("reported context = %q@%q, want alice@workstation", payload.LocalUsername, payload.LocalHostname)
			}
			if payload.CertificateType != string(certType) {
				t.Errorf("CertificateType = %q, want %q", payload.CertificateType, certType)
			}
		})
	}
}

// A service certificate's notification is the redemption one, addressed to
// the enrollment. A second message per redemption addressed to whoever
// approved the enrollment months ago would be noise about a job nobody was
// present for.
func TestResolveSuccess_shouldNotNotifyForAServiceCertificate(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypeService)

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypeService)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v for a service certificate, want nothing", got)
	}
}

// A request that was never bound to a user still yields a certificate and an
// audit row; there is simply nobody to tell about it.
func TestResolveSuccess_shouldNotNotifyWhenTheRequestHasNoOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)
	h := newTestSignedReplyHandler(t, svc)

	requestID := mustCreateUserRequest(t, svc)

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypeUser)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v for an unowned request, want nothing", got)
	}
}

// The critical options are read off the reply, not the request: what the
// reader wants confirmed is what the certificate carries.
func TestResolveSuccess_shouldReportTheGrantedCriticalOptions(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypeUser)

	reply := successfulReply(requestID, model.CertificateTypeUser)
	reply.CriticalOptions["force-command"] = "/usr/bin/true"

	if err := h.resolveSuccess(context.Background(), reply); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	payload, ok := notifier.only(t, notify.KindUserCertificateIssued).Payload.(*notify.CertificateIssued)
	if !ok {
		t.Fatal("payload is not *notify.CertificateIssued")
	}
	if payload.ForceCommand != "/usr/bin/true" {
		t.Errorf("ForceCommand = %q, want the option the certificate carries", payload.ForceCommand)
	}
	if len(payload.SourceAddresses) != 2 {
		t.Errorf("SourceAddresses = %v, want the comma-joined option split into its networks", payload.SourceAddresses)
	}
}

// A certificate with no source-address option is usable anywhere, which the
// message says by carrying no addresses rather than one empty string.
func TestResolveSuccess_shouldCarryNoAddressesForAnUnrestrictedCertificate(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypeUser)

	reply := successfulReply(requestID, model.CertificateTypeUser)
	reply.CriticalOptions = map[string]string{}

	if err := h.resolveSuccess(context.Background(), reply); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	payload, ok := notifier.only(t, notify.KindUserCertificateIssued).Payload.(*notify.CertificateIssued)
	if !ok {
		t.Fatal("payload is not *notify.CertificateIssued")
	}
	if len(payload.SourceAddresses) != 0 {
		t.Errorf("SourceAddresses = %v, want none for an unrestricted certificate", payload.SourceAddresses)
	}
}

// All three kinds default off so an existing deployment stays exactly as
// quiet on upgrade as it was before. Asserted against the registry rather
// than the emit path, because the default is what decides that.
func TestCertificateIssuedKinds_shouldDefaultOff(t *testing.T) {
	t.Parallel()

	for _, kind := range []notify.Kind{
		notify.KindUserCertificateIssued,
		notify.KindPAMCertificateIssued,
		notify.KindConsoleCertificateIssued,
	} {
		if notify.DefaultEnabled(kind) {
			t.Errorf("%s defaults on; one message per login or per sudo must be opt-in", kind)
		}
	}
}

// The message has to answer "was this you?", and neither half of that answer
// was in it before: what asked, and who let it happen.

// issuedReplyWithContext is issuedReplyFixture with the request carrying a
// full host-context report and a decision record behind it, which is the
// shape a real approval leaves.
func issuedReplyWithContext(t *testing.T) (*SignedReplyHandler, *capturingNotifier, *CertRequestService, string) {
	t.Helper()

	h, notifier, svc, requestID, _ := issuedReplyFixture(t, model.CertificateTypePAM)

	uid, gid, pid, ppid := int64(0), int64(0), int64(4412), int64(4200)
	if err := svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
		Updates(map[string]any{
			"pam_service": "sudo", "tty": "pts/3", "remote_host": "203.0.113.9",
			"requesting_user": "bob", "process": "sudo systemctl restart nginx",
			"machine_id": "3f2c1e0d", "os": "Debian GNU/Linux 13", "client": "pam_ssoossh-c/0.3.0",
			"client_mode": "auto", "caller_uid": uid, "caller_gid": gid,
			"caller_pid": pid, "caller_ppid": ppid,
		}).Error; err != nil {
		t.Fatalf("failed to write the host context: %v", err)
	}

	if err := svc.db.Create(&model.CertificateRequestDecision{
		ID:                   "decision-notify",
		CertificateRequestID: requestID,
		Outcome:              model.CertificateRequestDecisionApproved,
		Username:             "mike.nestor",
		Email:                "mike@example.org",
		SourceIP:             "203.0.113.9",
		UserAgent:            "Mozilla/5.0 (approver)",
		DecidedAt:            time.Date(2026, 9, 5, 21, 30, 0, 0, time.UTC),
	}).Error; err != nil {
		t.Fatalf("failed to write the decision: %v", err)
	}

	return h, notifier, svc, requestID
}

// issuedPayload runs the reply and returns the notification's payload.
func issuedPayload(t *testing.T, h *SignedReplyHandler, notifier *capturingNotifier, requestID string) *notify.CertificateIssued {
	t.Helper()

	if err := h.resolveSuccess(context.Background(), successfulReply(requestID, model.CertificateTypePAM)); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}
	got := notifier.only(t, notify.KindPAMCertificateIssued)
	payload, ok := got.Payload.(*notify.CertificateIssued)
	if !ok {
		t.Fatalf("payload is %T, want *notify.CertificateIssued", got.Payload)
	}
	return payload
}

func TestResolveSuccess_shouldCarryTheHostContextIntoTheNotification(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID := issuedReplyWithContext(t)
	payload := issuedPayload(t, h, notifier, requestID)

	for field, pair := range map[string][2]string{
		"RequestingUser": {payload.RequestingUser, "bob"},
		"Process":        {payload.Process, "sudo systemctl restart nginx"},
		"TTY":            {payload.TTY, "pts/3"},
		"RemoteHost":     {payload.RemoteHost, "203.0.113.9"},
		"PAMService":     {payload.PAMService, "sudo"},
		"MachineID":      {payload.MachineID, "3f2c1e0d"},
		"OS":             {payload.OS, "Debian GNU/Linux 13"},
		"Client":         {payload.Client, "pam_ssoossh-c/0.3.0"},
		"ClientMode":     {payload.ClientMode, "auto"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", field, pair[0], pair[1])
		}
	}
	// uid 0 is a value, which is why these are pointers: a root sudo must
	// not report "no uid".
	if payload.CallerUID == nil || *payload.CallerUID != 0 {
		t.Errorf("CallerUID = %v, want 0 rather than absent", payload.CallerUID)
	}
	if payload.CallerPID == nil || *payload.CallerPID != 4412 {
		t.Errorf("CallerPID = %v, want 4412", payload.CallerPID)
	}
}

// The most security-relevant thing in the message, and the thing it carried
// none of: on the unhappy path the approver's address is what says whether
// the reader's own account was used or someone else's.
func TestResolveSuccess_shouldCarryTheApproverIntoTheNotification(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID := issuedReplyWithContext(t)
	payload := issuedPayload(t, h, notifier, requestID)

	if payload.ApprovedByUsername != "mike.nestor" {
		t.Errorf("ApprovedByUsername = %q, want mike.nestor", payload.ApprovedByUsername)
	}
	if payload.ApprovedByEmail != "mike@example.org" {
		t.Errorf("ApprovedByEmail = %q, want mike@example.org", payload.ApprovedByEmail)
	}
	if payload.ApproverSourceIP != "203.0.113.9" {
		t.Errorf("ApproverSourceIP = %q, want the approver's address", payload.ApproverSourceIP)
	}
	if payload.ApproverUserAgent != "Mozilla/5.0 (approver)" {
		t.Errorf("ApproverUserAgent = %q, want the approver's browser", payload.ApproverUserAgent)
	}
	if payload.ApprovedAt.IsZero() {
		t.Error("ApprovedAt is zero; the templates read it to tell recorded from not recorded")
	}
	// The requester's address and the approver's are different questions and
	// must not collapse into one.
	if payload.SourceIP == payload.ApproverSourceIP {
		t.Errorf("SourceIP and ApproverSourceIP are both %q; they are different addresses", payload.SourceIP)
	}
}

// A certificate whose decision has gone still notifies. The approval block
// stays empty, which the templates render as "not recorded" -- inventing an
// approver would be worse than saying nothing.
func TestResolveSuccess_shouldNotifyWithoutADecisionRecord(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypePAM)
	payload := issuedPayload(t, h, notifier, requestID)

	if payload.ApprovedByUsername != "" || payload.ApproverSourceIP != "" {
		t.Errorf("approval block = %q/%q, want empty when no decision survives",
			payload.ApprovedByUsername, payload.ApproverSourceIP)
	}
	if !payload.ApprovedAt.IsZero() {
		t.Errorf("ApprovedAt = %v, want zero when no decision survives", payload.ApprovedAt)
	}
}

// no-touch-required is an extension, not a critical option: reading it from
// the wrong half of the reply would report every certificate as touch-free.
func TestResolveSuccess_shouldReportNoTouchRequiredFromTheExtensions(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypePAM)

	reply := successfulReply(requestID, model.CertificateTypePAM)
	reply.Extensions = []string{"permit-pty", "no-touch-required"}
	if err := h.resolveSuccess(context.Background(), reply); err != nil {
		t.Fatalf("resolveSuccess: %v", err)
	}

	got := notifier.only(t, notify.KindPAMCertificateIssued)
	payload, ok := got.Payload.(*notify.CertificateIssued)
	if !ok {
		t.Fatalf("payload is %T, want *notify.CertificateIssued", got.Payload)
	}
	if !payload.NoTouchRequired {
		t.Error("NoTouchRequired is false, want true when the extension was granted")
	}
}

func TestResolveSuccess_shouldNotReportNoTouchRequiredWhenItWasNotGranted(t *testing.T) {
	t.Parallel()

	h, notifier, _, requestID, _ := issuedReplyFixture(t, model.CertificateTypePAM)
	payload := issuedPayload(t, h, notifier, requestID)

	if payload.NoTouchRequired {
		t.Error("NoTouchRequired is true, want false when the extension was not granted")
	}
}
