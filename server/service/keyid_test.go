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
