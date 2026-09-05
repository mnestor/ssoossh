package service

// Test methodology: Unit tests for per-type policy resolution
// (newCertTypePolicies) and narrowing (narrowRequestedOptions). Table-driven
// where appropriate. Tests run in parallel (t.Parallel()).

import (
	"slices"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

func mustCertTypePolicies(t *testing.T, opts config.CertificateOptions, declaredClaims ...map[string]string) map[model.CertificateType]*certTypePolicy {
	t.Helper()

	kt, err := newKeyIDTemplates(opts)
	if err != nil {
		t.Fatalf("newKeyIDTemplates() error = %v", err)
	}
	var claims map[string]string
	if len(declaredClaims) > 0 {
		claims = declaredClaims[0]
	}
	policies, err := newCertTypePolicies(opts, kt, claims)
	if err != nil {
		t.Fatalf("newCertTypePolicies() error = %v", err)
	}
	return policies
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
		PAM: config.CertOptionsPAM{
			Require:       &config.PolicyCondition{Group: "sudoers"},
			ValidDuration: 30 * time.Second,
		},
	})
	pam := policies[model.CertificateTypePAM]

	narrowed := narrowRequestedOptions(pam, RequestedOptions{Extensions: []string{"permit-pty"}})
	if narrowed.Extensions != nil {
		t.Errorf("expected no PAM extensions to survive narrowing, got %v", narrowed.Extensions)
	}
	if pam.validDuration != 30*time.Second {
		t.Errorf("got ValidDuration %v, want PAM's own 30s, not User's", pam.validDuration)
	}
	if pam.require == nil || !pam.require.evaluate(&Identity{Groups: []string{"sudoers"}}) {
		t.Error("expected PAM's require condition to admit a sudoers member")
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

func TestNewCertTypePolicies_Principals_ShouldUseUsernameForUserAndServiceCertificates(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	for _, certType := range []model.CertificateType{model.CertificateTypeUser, model.CertificateTypeService} {
		got := policies[certType].principals(&Identity{Username: "alice"}, nil)
		if len(got) != 1 || got[0] != "alice" {
			t.Errorf("for %s: got %v, want [\"alice\"]", certType, got)
		}
	}
}

// TestNewCertTypePolicies_Principals_ShouldDefaultToApproverAccountsForLocalAuth
// is the assertion this inverted: a PAM certificate used to name the local
// account the module sent, which made an unauthenticated caller the author
// of the field the certificate is authorized on. With no selection it now
// names every account the approver holds, and pam_ssoossh's check 3 matches
// those against the local account on the host. See
// docs/proposals/pam-principal-source.md.
func TestNewCertTypePolicies_Principals_ShouldDefaultToApproverAccountsForLocalAuth(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	for _, certType := range []model.CertificateType{model.CertificateTypePAM, model.CertificateTypeConsole} {
		identity := &Identity{Username: "mike.nestor", OtherAccounts: []string{"mnestor", "root"}}
		got := policies[certType].principals(identity, nil)

		want := []string{"mike.nestor", "mnestor", "root"}
		if !slices.Equal(got, want) {
			t.Errorf("for %s: got %v, want %v", certType, got, want)
		}
	}
}

// The approver picks which of their accounts a PAM or console certificate
// carries, the same way they do for a user certificate. The selection is
// taken as given here; keeping it inside what they hold is the linkage
// check's job, asserted separately below.
func TestNewCertTypePolicies_Principals_ShouldUseTheSelectionForLocalAuth(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	for _, certType := range []model.CertificateType{model.CertificateTypePAM, model.CertificateTypeConsole} {
		identity := &Identity{Username: "mike.nestor", OtherAccounts: []string{"mnestor", "root"}}
		got := policies[certType].principals(identity, []string{"mnestor"})

		want := []string{"mnestor"}
		if !slices.Equal(got, want) {
			t.Errorf("for %s: got %v, want %v", certType, got, want)
		}
	}
}

// A selection is only as safe as the check that it stays inside what the
// approver holds. Every type that lets the approver pick has to carry
// that check, or a caller could name any account at all.
func TestNewCertTypePolicies_Linkage_ShouldRefuseAnUnheldPrincipalForEveryPickingType(t *testing.T) {
	t.Parallel()

	policies := mustCertTypePolicies(t, config.CertificateOptions{})

	tests := []struct {
		name     string
		certType model.CertificateType
		selected []string
		wantErr  bool
	}{
		{name: "should accept a held account for user", certType: model.CertificateTypeUser, selected: []string{"mnestor"}},
		{name: "should refuse an unheld account for user", certType: model.CertificateTypeUser, selected: []string{"root"}, wantErr: true},
		{name: "should accept a held account for pam", certType: model.CertificateTypePAM, selected: []string{"mnestor"}},
		{name: "should refuse an unheld account for pam", certType: model.CertificateTypePAM, selected: []string{"root"}, wantErr: true},
		{name: "should accept a held account for console", certType: model.CertificateTypeConsole, selected: []string{"mnestor"}},
		{name: "should refuse an unheld account for console", certType: model.CertificateTypeConsole, selected: []string{"root"}, wantErr: true},
		{name: "should accept an empty selection for pam", certType: model.CertificateTypePAM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy := policies[tt.certType]
			if policy.linkage == nil {
				t.Fatalf("%s has no linkage check; a picking type must have one", tt.certType)
			}
			identity := &Identity{Username: "mike.nestor", OtherAccounts: []string{"mnestor"}}
			err := policy.linkage(identity, ApprovalSelection{Principals: tt.selected})
			if (err != nil) != tt.wantErr {
				t.Errorf("linkage(%v) error = %v, wantErr %t", tt.selected, err, tt.wantErr)
			}
		})
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
