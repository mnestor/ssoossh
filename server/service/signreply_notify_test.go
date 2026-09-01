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
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:          certType,
		PublicKey:     "ssh-ed25519 AAAA test",
		SourceIP:      "198.51.100.7",
		LocalUsername: "alice",
		LocalHostname: "workstation",
	})
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

// Both kinds default off so an existing deployment stays exactly as quiet on
// upgrade as it was before. Asserted against the registry rather than the
// emit path, because the default is what decides that.
func TestCertificateIssuedKinds_shouldDefaultOff(t *testing.T) {
	t.Parallel()

	for _, kind := range []notify.Kind{notify.KindUserCertificateIssued, notify.KindPAMCertificateIssued} {
		if notify.DefaultEnabled(kind) {
			t.Errorf("%s defaults on; one message per login or per sudo must be opt-in", kind)
		}
	}
}
