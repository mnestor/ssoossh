package service

// Test methodology: Unit tests for key ID template parsing/execution and
// its per-type fallback rule. Table-driven for the fallback matrix. Tests
// run in parallel (t.Parallel()).

import (
	"testing"
	"text/template"

	"github.com/mnestor/ssoossh/server/config"
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
// Service/PAM error branches specifically — each needs every template
// validated before it (User, then Service) to be well-formed, or the
// earlier one's error would mask this one's.
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
		field     func(*keyIDTemplates) *template.Template
		wantKeyID string
	}{
		{
			name:      "should use the default username template when nothing is configured",
			opts:      config.CertificateOptions{},
			field:     func(t *keyIDTemplates) *template.Template { return t.user },
			wantKeyID: "alice",
		},
		{
			name: "should fall back service to the configured user template",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
			},
			field:     func(t *keyIDTemplates) *template.Template { return t.service },
			wantKeyID: "u:alice",
		},
		{
			name: "should not fall back when a type has its own template",
			opts: config.CertificateOptions{
				User:    config.CertOptionsUser{KeyIDTemplate: "u:{{.Username}}"},
				Service: config.CertOptionsService{KeyIDTemplate: "s:{{.Username}}"},
			},
			field:     func(t *keyIDTemplates) *template.Template { return t.service },
			wantKeyID: "s:alice",
		},
		{
			name:      "should use PAM's own default template when nothing is configured",
			opts:      config.CertificateOptions{},
			field:     func(t *keyIDTemplates) *template.Template { return t.pam },
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
			field:     func(t *keyIDTemplates) *template.Template { return t.pam },
			wantKeyID: "pam:alice",
		},
		{
			name: "should use PAM's own configured template when set",
			opts: config.CertificateOptions{
				PAM: config.CertOptionsPAM{KeyIDTemplate: "p:{{.Username}}"},
			},
			field:     func(t *keyIDTemplates) *template.Template { return t.pam },
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

			got, err := executeKeyIDTemplate(tt.field(tmpls), keyIDTemplateData{Username: "alice"})
			if err != nil {
				t.Fatalf("unexpected error executing template: %v", err)
			}
			if got != tt.wantKeyID {
				t.Errorf("got key ID %q, want %q", got, tt.wantKeyID)
			}
		})
	}
}
