package controller

import (
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// A history row leads with what asked for the certificate, so a list row
// carries the decision's reported "user@host" -- and only that pair. The
// rest of the host-context snapshot belongs to the detail endpoint, which
// is what setReportedContextOnCertificate is for.
func TestNewCertificateResponsesWithDecisions_ReportedContext(t *testing.T) {
	issued := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	got := newCertificateResponsesWithDecisions([]service.CertificateWithDecision{{
		Certificate: model.Certificate{
			ID:       "cert-1",
			Type:     model.CertificateTypePAM,
			KeyID:    "alice",
			IssuedAt: issued,
		},
		Decision: &model.CertificateRequestDecision{
			Outcome:          model.CertificateRequestDecisionApproved,
			Subject:          "sub-approver",
			Username:         "approver",
			Email:            "approver@example.com",
			ReportedUsername: "root",
			ReportedHostname: "web01",
			PAMService:       "sshd",
			TTY:              "pts/0",
			RemoteHost:       "198.51.100.9",
			RequestingUser:   "alice",
			Process:          "/usr/sbin/sshd",
			MachineID:        "mid-1",
			Client:           "ssoossh-pam/1.0",
			DecidedAt:        issued,
		},
	}})
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	row := got[0]

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"should carry the reported username a row leads with", row.ReportedUsername, "root"},
		{"should carry the reported hostname a row leads with", row.ReportedHostname, "web01"},
		{"should leave the pam service to the detail endpoint", row.ReportedService, ""},
		{"should leave the terminal to the detail endpoint", row.ReportedTTY, ""},
		{"should leave the remote host to the detail endpoint", row.ReportedRemoteHost, ""},
		{"should leave the invoking user to the detail endpoint", row.ReportedRequestingUser, ""},
		{"should leave the command to the detail endpoint", row.ReportedProcess, ""},
		{"should leave the machine id to the detail endpoint", row.ReportedMachineID, ""},
		{"should leave the client to the detail endpoint", row.ReportedClient, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// A row with no decision behind it has nothing to report, and must not
// invent an empty "user@host" the UI would then have to tell apart from a
// real one.
func TestNewCertificateResponsesWithDecisions_ShouldOmitReportedContextWithoutADecision(t *testing.T) {
	got := newCertificateResponsesWithDecisions([]service.CertificateWithDecision{{
		Certificate: model.Certificate{ID: "cert-1", Type: model.CertificateTypeUser, KeyID: "alice"},
	}})

	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].ReportedUsername != "" || got[0].ReportedHostname != "" {
		t.Errorf("got reported identity %q@%q, want both empty",
			got[0].ReportedUsername, got[0].ReportedHostname)
	}
}
