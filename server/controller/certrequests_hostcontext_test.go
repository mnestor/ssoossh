package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/model"
)

// The PAM and console bodies carry the same host context, and both
// handlers must hand every field to the service: a field the handler
// drops is one the approval page and the audit trail never see.

const hostContextBody = `{"public_key":"ssh-ed25519 AAAA... host","username":"root",` +
	`"hostname":"web01","pam_service":"sudo","tty":"pts/3","remote_host":"",` +
	`"requesting_user":"alice","process":"sudo -i",` +
	`"caller_uid":1000,"caller_pid":4242,"caller_ppid":4200,` +
	`"machine_id":"3f2c1e0d9b8a7f6e","os":"Debian GNU/Linux 13 (trixie) Linux 6.12.0",` +
	`"client":"pam_ssoossh-c/0.3.0","mode":"auto","client_time":"2026-09-05T13:04:05Z",` +
	`"trusted_ca_fingerprints":["SHA256:aaa","SHA256:bbb"]}`

func TestCreateRequestHandlers_ShouldPassTheHostContextToTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		wantType model.CertificateType
	}{
		{name: "should pass it for a pam request", path: "/certs/pam", wantType: model.CertificateTypePAM},
		{name: "should pass it for a console request", path: "/certs/console", wantType: model.CertificateTypeConsole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gin.SetMode(gin.TestMode)
			svc := &fakeCertRequestService{createRequestID: "req-host"}

			r := gin.New()
			NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(hostContextBody))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
			}
			if svc.gotParams.Type != tt.wantType {
				t.Errorf("got request type %q, want %q", svc.gotParams.Type, tt.wantType)
			}

			hc := svc.gotParams.HostContext
			for _, field := range []struct {
				name string
				got  string
				want string
			}{
				{name: "Hostname", got: svc.gotParams.Hostname, want: "web01"},
				{name: "PAMService", got: svc.gotParams.PAMService, want: "sudo"},
				{name: "TTY", got: svc.gotParams.TTY, want: "pts/3"},
				{name: "RequestingUser", got: hc.RequestingUser, want: "alice"},
				{name: "Process", got: hc.Process, want: "sudo -i"},
				{name: "MachineID", got: hc.MachineID, want: "3f2c1e0d9b8a7f6e"},
				{name: "OS", got: hc.OS, want: "Debian GNU/Linux 13 (trixie) Linux 6.12.0"},
				{name: "Client", got: hc.Client, want: "pam_ssoossh-c/0.3.0"},
				{name: "Mode", got: hc.Mode, want: "auto"},
			} {
				if field.got != field.want {
					t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
				}
			}
			for _, field := range []struct {
				name string
				got  *int64
				want int64
			}{
				{name: "CallerUID", got: hc.CallerUID, want: 1000},
				{name: "CallerPID", got: hc.CallerPID, want: 4242},
				{name: "CallerPPID", got: hc.CallerPPID, want: 4200},
			} {
				if field.got == nil {
					t.Errorf("%s is nil, want %d", field.name, field.want)
				} else if *field.got != field.want {
					t.Errorf("%s = %d, want %d", field.name, *field.got, field.want)
				}
			}
			if hc.ClientTime == nil || !hc.ClientTime.Equal(time.Date(2026, 9, 5, 13, 4, 5, 0, time.UTC)) {
				t.Errorf("ClientTime = %v, want 2026-09-05T13:04:05Z", hc.ClientTime)
			}
			if len(hc.TrustedCAFingerprints) != 2 || hc.TrustedCAFingerprints[0] != "SHA256:aaa" {
				t.Errorf("TrustedCAFingerprints = %v, want the two sent", hc.TrustedCAFingerprints)
			}
		})
	}
}

// A body with none of the context is still a request: every context field
// is optional, and an absent integer must arrive as nil rather than zero.
func TestCreatePAMRequestHandler_ShouldLeaveAbsentContextNil(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-bare"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/pam",
		strings.NewReader(`{"public_key":"ssh-ed25519 AAAA... bare","username":"root"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	hc := svc.gotParams.HostContext
	if hc.CallerUID != nil || hc.CallerPID != nil || hc.CallerPPID != nil || hc.ClientTime != nil {
		t.Errorf("expected absent integers and time to stay nil, got %+v", hc)
	}
	if hc.TrustedCAFingerprints != nil {
		t.Errorf("expected no fingerprints, got %v", hc.TrustedCAFingerprints)
	}
}

// A user request carries the same host context a PAM one does, minus the two
// fields it has no analogue for. Until the Go client sent it, a user
// certificate reached the approval page and the audit trail with two strings
// where a sudo reached them with a dozen.
const userHostContextBody = `{"public_key":"ssh-ed25519 AAAA... test",` +
	`"local_username":"alice","local_hostname":"alice-laptop",` +
	`"requesting_user":"bob","process":"ssoossh ssh login","tty":"/dev/pts/3",` +
	`"remote_host":"203.0.113.9",` +
	`"caller_uid":501,"caller_gid":20,"caller_pid":4412,"caller_ppid":4200,` +
	`"machine_id":"3f2c1e0d9b8a7f6e","os":"macOS 26.5.2 Darwin 25.5.0",` +
	`"client":"ssoossh/1.2.3","client_time":"2026-09-05T21:30:00Z",` +
	`"trusted_ca_fingerprints":["SHA256:pinned"]}`

func TestCreateUserRequestHandler_ShouldPassTheHostContextToTheService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-user-host"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader(userHostContextBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotParams.Type != model.CertificateTypeUser {
		t.Errorf("got request type %q, want %q", svc.gotParams.Type, model.CertificateTypeUser)
	}

	hc := svc.gotParams.HostContext
	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		// A user request's identity is the local pair, not the PAM one:
		// model.CertificateRequest.ReportedIdentity picks between them by
		// type, and filling the PAM columns here would make it pick wrong.
		{name: "LocalUsername", got: svc.gotParams.LocalUsername, want: "alice"},
		{name: "LocalHostname", got: svc.gotParams.LocalHostname, want: "alice-laptop"},
		{name: "Username", got: svc.gotParams.Username, want: ""},
		{name: "Hostname", got: svc.gotParams.Hostname, want: ""},
		// The terminal and the peer share the PAM columns: a terminal is a
		// terminal whichever implementation reported it.
		{name: "TTY", got: svc.gotParams.TTY, want: "/dev/pts/3"},
		{name: "RemoteHost", got: svc.gotParams.RemoteHost, want: "203.0.113.9"},
		// Neither has an analogue for a CLI, and both must stay empty
		// rather than borrow something from another type.
		{name: "PAMService", got: svc.gotParams.PAMService, want: ""},
		{name: "Mode", got: hc.Mode, want: ""},
		{name: "RequestingUser", got: hc.RequestingUser, want: "bob"},
		{name: "Process", got: hc.Process, want: "ssoossh ssh login"},
		{name: "MachineID", got: hc.MachineID, want: "3f2c1e0d9b8a7f6e"},
		{name: "OS", got: hc.OS, want: "macOS 26.5.2 Darwin 25.5.0"},
		{name: "Client", got: hc.Client, want: "ssoossh/1.2.3"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	for _, field := range []struct {
		name string
		got  *int64
		want int64
	}{
		{name: "CallerUID", got: hc.CallerUID, want: 501},
		{name: "CallerGID", got: hc.CallerGID, want: 20},
		{name: "CallerPID", got: hc.CallerPID, want: 4412},
		{name: "CallerPPID", got: hc.CallerPPID, want: 4200},
	} {
		if field.got == nil {
			t.Errorf("%s is nil, want %d", field.name, field.want)
		} else if *field.got != field.want {
			t.Errorf("%s = %d, want %d", field.name, *field.got, field.want)
		}
	}
	if hc.ClientTime == nil || !hc.ClientTime.Equal(time.Date(2026, 9, 5, 21, 30, 0, 0, time.UTC)) {
		t.Errorf("ClientTime = %v, want 2026-09-05T21:30:00Z", hc.ClientTime)
	}
	if len(hc.TrustedCAFingerprints) != 1 || hc.TrustedCAFingerprints[0] != "SHA256:pinned" {
		t.Errorf("TrustedCAFingerprints = %v, want the pinned one", hc.TrustedCAFingerprints)
	}
}

// An older client, or one on a platform that can answer none of it, still
// creates a request; every context field is optional and an absent integer
// arrives as nil rather than zero.
func TestCreateUserRequestHandler_ShouldLeaveAbsentContextNil(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-user-bare"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user",
		strings.NewReader(`{"public_key":"ssh-ed25519 AAAA... test","local_username":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	hc := svc.gotParams.HostContext
	if hc.CallerUID != nil || hc.CallerGID != nil || hc.CallerPID != nil || hc.CallerPPID != nil {
		t.Errorf("caller ids = %v/%v/%v/%v, want all nil when none was reported",
			hc.CallerUID, hc.CallerGID, hc.CallerPID, hc.CallerPPID)
	}
	if hc.ClientTime != nil {
		t.Errorf("ClientTime = %v, want nil", hc.ClientTime)
	}
	if hc.MachineID != "" || hc.Client != "" || hc.Process != "" {
		t.Errorf("machine_id/client/process = %q/%q/%q, want all empty",
			hc.MachineID, hc.Client, hc.Process)
	}
}
