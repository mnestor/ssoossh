package config

import "testing"

// TestSupportContact pins the one support-contact setting: the branding
// address everywhere, with the superseded admin.contact_email read only
// when that is unset.
func TestSupportContact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      *Config
		wantAddress string
		wantLabel   string
	}{
		{name: "should return nothing when neither is set", config: &Config{}},
		{
			name:        "should return the branding address and label",
			config:      &Config{Branding: BrandingSettings{SupportEmail: "help@corp.example", SupportLabel: "the IT desk"}},
			wantAddress: "help@corp.example",
			wantLabel:   "the IT desk",
		},
		{
			name:        "should prefer the branding address over admin.contact_email",
			config:      &Config{Branding: BrandingSettings{SupportEmail: "help@corp.example"}, Admin: AdminConfig{ContactEmail: "old@corp.example"}},
			wantAddress: "help@corp.example",
		},
		{
			name:        "should fall back to admin.contact_email without a label",
			config:      &Config{Admin: AdminConfig{ContactEmail: "old@corp.example"}},
			wantAddress: "old@corp.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			address, label := tt.config.SupportContact()
			if address != tt.wantAddress || label != tt.wantLabel {
				t.Errorf("SupportContact() = (%q, %q), want (%q, %q)", address, label, tt.wantAddress, tt.wantLabel)
			}
		})
	}
}
