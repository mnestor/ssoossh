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

// TestNewKeyIDTemplates_ShouldNotErrorOnUnknownExtraName is the deliberate
// counterpart to ShouldErrorOnUnknownField: struct field typos fail startup,
// but .Extra lookups never do — an unconfigured name renders MISSING at
// issuance instead (missingkey=zero over the Extra map).
func TestNewKeyIDTemplates_ShouldNotErrorOnUnknownExtraName(t *testing.T) {
	t.Parallel()

	_, err := newKeyIDTemplates(config.CertificateOptions{
		User: config.CertOptionsUser{KeyIDTemplate: "{{.Extra.notconfigured}}"},
	})
	if err != nil {
		t.Fatalf("expected no error for an unknown extra name, got %v", err)
	}
}

func TestExecuteKeyIDTemplate_ExtraFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     keyIDTemplateData
		want     string
	}{
		{
			name:     "should render a scalar extra field",
			template: "{{.Username}}-{{.Extra.dept}}",
			data: keyIDTemplateData{
				Username: "alice",
				Extra:    map[string]extraValue{"dept": scalarExtra("eng")},
			},
			want: "alice-eng",
		},
		{
			name:     "should render a list extra field comma-joined by default",
			template: "{{.Extra.accounts}}",
			data: keyIDTemplateData{
				Extra: map[string]extraValue{"accounts": listExtra([]string{"a", "b"})},
			},
			want: "a,b",
		},
		{
			name:     "should join a list extra field with the given separator",
			template: `{{join .Extra.accounts ";"}}`,
			data: keyIDTemplateData{
				Extra: map[string]extraValue{"accounts": listExtra([]string{"a", "b"})},
			},
			want: "a;b",
		},
		{
			name:     "should render MISSING for an extra name absent from the map",
			template: "{{.Extra.bogus}}",
			data:     keyIDTemplateData{Extra: map[string]extraValue{}},
			want:     "MISSING",
		},
		{
			name:     "should render MISSING for an extra lookup on a nil map",
			template: "{{.Extra.bogus}}",
			data:     keyIDTemplateData{},
			want:     "MISSING",
		},
		{
			name:     "should render MISSING when joining an absent extra name",
			template: `{{join .Extra.bogus ";"}}`,
			data:     keyIDTemplateData{},
			want:     "MISSING",
		},
		{
			name:     "should render MISSING for an extra stored empty at login",
			template: "{{.Extra.dept}}",
			data: keyIDTemplateData{
				Extra: map[string]extraValue{"dept": scalarExtra("")},
			},
			want: "MISSING",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpls, err := newKeyIDTemplates(config.CertificateOptions{
				User: config.CertOptionsUser{KeyIDTemplate: tt.template},
			})
			if err != nil {
				t.Fatalf("unexpected error parsing template: %v", err)
			}

			got, err := executeKeyIDTemplate(tmpls.user, tt.data)
			if err != nil {
				t.Fatalf("unexpected error executing template: %v", err)
			}
			if got != tt.want {
				t.Errorf("got key ID %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewKeyIDTemplateData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity Identity
		clientIP string
		uniqueID string
		want     keyIDTemplateData
	}{
		{
			name: "should pass populated fields through unchanged",
			identity: Identity{
				Username: "alice",
				Subject:  "sub-1",
				Email:    "alice@example.com",
				Extra:    map[string]extraValue{"dept": scalarExtra("eng")},
			},
			clientIP: "10.0.0.1",
			uniqueID: "req-1",
			want: keyIDTemplateData{
				Username: "alice",
				Subject:  "sub-1",
				Email:    "alice@example.com",
				ClientIP: "10.0.0.1",
				UniqueID: "req-1",
				Extra:    map[string]extraValue{"dept": scalarExtra("eng")},
			},
		},
		{
			name:     "should substitute MISSING for every empty standard field",
			identity: Identity{},
			want: keyIDTemplateData{
				Username: "MISSING",
				Subject:  "MISSING",
				Email:    "MISSING",
				ClientIP: "MISSING",
				UniqueID: "MISSING",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := newKeyIDTemplateData(&tt.identity, tt.clientIP, tt.uniqueID)
			if got.Username != tt.want.Username || got.Subject != tt.want.Subject ||
				got.Email != tt.want.Email || got.ClientIP != tt.want.ClientIP ||
				got.UniqueID != tt.want.UniqueID {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if len(got.Extra) != len(tt.want.Extra) {
				t.Errorf("got Extra %+v, want %+v", got.Extra, tt.want.Extra)
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
