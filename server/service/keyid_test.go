package service

// Test methodology: Unit tests for key ID template parsing/execution and
// its per-type fallback rule. Table-driven for the fallback matrix. Tests
// run in parallel (t.Parallel()).

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

func TestNewKeyIDTemplates_ShouldErrorOnInvalidSyntax(t *testing.T) {
	t.Parallel()

	_, err := newKeyIDTemplates(config.CertificateOptions{
		User: config.CertOptionsUser{KeyIDTemplate: "{{.Username"},
	})
	if err == nil {
		t.Fatal("expected an error for malformed template syntax, got nil")
	}
}

func TestNewKeyIDTemplates_ShouldErrorOnUnknownField(t *testing.T) {
	t.Parallel()

	_, err := newKeyIDTemplates(config.CertificateOptions{
		User: config.CertOptionsUser{KeyIDTemplate: "{{.Bogus}}"},
	})
	if err == nil {
		t.Fatal("expected an error for an unrecognized template field, got nil")
	}
}

// TestNewKeyIDTemplates_ShouldErrorOnEachPerTypeTemplate covers the
// Service/Host/PAM error branches specifically — each needs every template
// validated before it (User, then Service, then Host) to be well-formed, or
// the earlier one's error would mask this one's.
func TestNewKeyIDTemplates_ShouldErrorOnEachPerTypeTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts config.CertificateOptions
	}{
		{
			name: "should error on a malformed Service template",
			opts: config.CertificateOptions{Service: config.CertOptionsService{KeyIDTemplate: "{{.Bogus}}"}},
		},
		{
			name: "should error on a malformed Host template",
			opts: config.CertificateOptions{Host: config.CertOptions{KeyIDTemplate: "{{.Bogus}}"}},
		},
		{
			name: "should error on a malformed PAM template",
			opts: config.CertificateOptions{PAM: config.CertOptionsPAM{KeyIDTemplate: "{{.Bogus}}"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newKeyIDTemplates(tt.opts); err == nil {
				t.Fatal("expected an error for an unrecognized template field, got nil")
			}
		})
	}
}

func TestNewKeyIDTemplates_FallbackChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      config.CertificateOptions
		certType  model.CertificateType
		wantKeyID string
	}{
		{
			name:      "should use the default username template when nothing is configured",
			opts:      config.CertificateOptions{},
			certType:  model.CertificateTypeUser,
			wantKeyID: "alice",
		},
		{
			name:      "should use the default hostname template for host when nothing is configured",
			opts:      config.CertificateOptions{},
			certType:  model.CertificateTypeHost,
			wantKeyID: "db01",
		},
		{
			name: "should fall back service to the configured user template",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
			},
			certType:  model.CertificateTypeService,
			wantKeyID: "u:alice",
		},
		{
			name: "should fall back host to the configured user template when host is unset",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
			},
			certType:  model.CertificateTypeHost,
			wantKeyID: "u:alice",
		},
		{
			name: "should not fall back when a type has its own template",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
				Host: config.CertOptions{KeyIDTemplate: "h:{{.Hostname}}"},
			},
			certType:  model.CertificateTypeHost,
			wantKeyID: "h:db01",
		},
		{
			name:      "should use PAM's own default template when nothing is configured",
			opts:      config.CertificateOptions{},
			certType:  model.CertificateTypePAM,
			wantKeyID: "pam:alice",
		},
		{
			// The one deliberate divergence from every other type: PAM must
			// NOT inherit the configured user template, so a sudo and a
			// login by the same person stay distinguishable in an audit
			// log even when an operator has customized the user template.
			name: "should NOT fall back PAM to the configured user template",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
			},
			certType:  model.CertificateTypePAM,
			wantKeyID: "pam:alice",
		},
		{
			name: "should use PAM's own configured template when set",
			opts: config.CertificateOptions{
				PAM: config.CertOptionsPAM{KeyIDTemplate: "p:{{.Username}}"},
			},
			certType:  model.CertificateTypePAM,
			wantKeyID: "p:alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpls, err := newKeyIDTemplates(tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := tmpls.execute(tt.certType, keyIDTemplateData{Username: "alice", Hostname: "db01"})
			if err != nil {
				t.Fatalf("unexpected error executing template: %v", err)
			}
			if got != tt.wantKeyID {
				t.Errorf("got key ID %q, want %q", got, tt.wantKeyID)
			}
		})
	}
}

func TestKeyIDTemplates_Execute_ShouldErrorForUnknownCertificateType(t *testing.T) {
	t.Parallel()

	tmpls, err := newKeyIDTemplates(config.CertificateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tmpls.execute(model.CertificateType("bogus"), keyIDTemplateData{})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected an error naming the unknown certificate type, got %v", err)
	}
}
