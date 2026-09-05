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
