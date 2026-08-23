package service

// Test methodology: Unit tests for per-type policy resolution
// (newCertTypePolicies) and narrowing (narrowRequestedOptions). Table-driven
// where appropriate. Tests run in parallel (t.Parallel()).

import (
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

func mustCertTypePolicies(t *testing.T, opts config.CertificateOptions) map[model.CertificateType]*certTypePolicy {
	t.Helper()

	kt, err := newKeyIDTemplates(opts)
	if err != nil {
		t.Fatalf("newKeyIDTemplates() error = %v", err)
	}
	return newCertTypePolicies(opts, kt)
}

// TestNewCertTypePolicies_ShouldNarrowPAMExtensionsAndUsePAMDuration
// confirms PAM reads from its own config section — cert_options.pam.extensions
// being empty (the documented default) drops a requested extension entirely
// rather than granting it, and PAM's ValidDuration is used rather than
// User's.
func TestNewCertTypePolicies_ShouldNarrowPAMExtensionsAndUsePAMDuration(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{
		User: config.CertOptionsUser{Extensions: []string{"permit-pty"}, ValidDuration: time.Hour},
		PAM:  config.CertOptionsPAM{RequireGroup: "sudoers", ValidDuration: 30 * time.Second},
	})
	pam := policies[model.CertificateTypePAM]

	narrowed := narrowRequestedOptions(pam, RequestedOptions{Extensions: []string{"permit-pty"}})
	if narrowed.Extensions != nil {
		t.Errorf("expected no PAM extensions to survive narrowing, got %v", narrowed.Extensions)
	}
	if pam.validDuration != 30*time.Second {
		t.Errorf("got ValidDuration %v, want PAM's own 30s, not User's", pam.validDuration)
	}
	if pam.requireGroup != "sudoers" {
		t.Errorf("got RequireGroup %q, want %q", pam.requireGroup, "sudoers")
	}
}

func TestNarrowRequestedOptions_ShouldDropForceCommandAndSourceAddresses(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{
		User: config.CertOptionsUser{Extensions: []string{"permit-pty"}},
	})

	narrowed := narrowRequestedOptions(policies[model.CertificateTypeUser], RequestedOptions{
		Extensions:      []string{"permit-pty"},
		ForceCommand:    "/bin/true",
		SourceAddresses: []string{"10.0.0.1"},
	})
	if narrowed.ForceCommand != "" {
		t.Errorf("expected ForceCommand to be dropped, got %q", narrowed.ForceCommand)
	}
	if narrowed.SourceAddresses != nil {
		t.Errorf("expected SourceAddresses to be dropped, got %v", narrowed.SourceAddresses)
	}
}

func TestNewCertTypePolicies_ShouldOnlyGrantNoTouchRequiredForService(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	tests := []struct {
		name     string
		certType model.CertificateType
		want     bool
	}{
		{"should grant for service", model.CertificateTypeService, true},
		{"should not grant for user", model.CertificateTypeUser, false},
		{"should not grant for host", model.CertificateTypeHost, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			narrowed := narrowRequestedOptions(policies[tt.certType], RequestedOptions{NoTouchRequired: true})
			if narrowed.NoTouchRequired != tt.want {
				t.Errorf("got NoTouchRequired %v, want %v", narrowed.NoTouchRequired, tt.want)
			}
		})
	}
}

func TestNewCertTypePolicies_Principals_ShouldUseHostnameForHostCertificates(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	got := policies[model.CertificateTypeHost].principals("db01.internal", "", &Identity{Username: "alice"})
	if len(got) != 1 || got[0] != "db01.internal" {
		t.Errorf("got %v, want [\"db01.internal\"]", got)
	}
}

func TestNewCertTypePolicies_Principals_ShouldUseUsernameForUserAndServiceCertificates(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	for _, certType := range []model.CertificateType{model.CertificateTypeUser, model.CertificateTypeService} {
		got := policies[certType].principals("db01.internal", "", &Identity{Username: "alice"})
		if len(got) != 1 || got[0] != "alice" {
			t.Errorf("for %s: got %v, want [\"alice\"]", certType, got)
		}
	}
}

// TestNewCertTypePolicies_Principals_ShouldUsePAMUsernameNotIdentity is the
// assertion that catches the wrong reading of
// the PAM principal-resolution design (docs/features.md, PAM): PAM
// certificates must name the local account the module authenticated, not
// the approver's OIDC identity, even when those two names differ.
func TestNewCertTypePolicies_Principals_ShouldUsePAMUsernameNotIdentity(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	got := policies[model.CertificateTypePAM].principals("", "mnestor", &Identity{Username: "mike.nestor"})
	if len(got) != 1 || got[0] != "mnestor" {
		t.Errorf("got %v, want [\"mnestor\"]", got)
	}
}

func TestNewCertTypePolicies_ShouldSetFlowPerType(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	tests := []struct {
		certType model.CertificateType
		want     certApprovalFlow
	}{
		{model.CertificateTypeUser, flowSigning},
		{model.CertificateTypePAM, flowSigning},
		{model.CertificateTypeService, flowEnrollment},
		{model.CertificateTypeHost, flowSigning},
	}
	for _, tt := range tests {
		t.Run(string(tt.certType), func(t *testing.T) {
			t.Parallel()

			if got := policies[tt.certType].flow; got != tt.want {
				t.Errorf("got flow %v, want %v", got, tt.want)
			}
		})
	}
}
