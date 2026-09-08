package cmd

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/client/config"
)

// pinnedCAFingerprints is the client's half of the module's
// trusted-ca-file report: it says "this client will refuse a certificate
// signed by anything but this key", so the approval page can warn before
// the refusal rather than after.
//
// The distinction that matters is pinned versus fetched. A key the runner
// pulled from this same server says nothing the server does not already
// know, and reporting one back as trust would be circular.
func TestPinnedCAFingerprints(t *testing.T) {
	t.Parallel()

	const caKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example"

	tests := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{
			name: "should report the fingerprint when the key was pinned",
			cfg:  &config.Config{CAPubkey: []string{caKey}, CAPubkeyPinned: true},
			want: 1,
		},
		{
			name: "should report nothing when the key was fetched from the server",
			cfg:  &config.Config{CAPubkey: []string{caKey}, CAPubkeyPinned: false},
			want: 0,
		},
		{
			name: "should report nothing when there is no key at all",
			cfg:  &config.Config{CAPubkeyPinned: true},
			want: 0,
		},
		// Audit metadata is never a reason to fail a login, so a key that
		// will not parse is reported as no fingerprint.
		{
			name: "should report nothing when the pinned key will not parse",
			cfg:  &config.Config{CAPubkey: []string{"not-a-key"}, CAPubkeyPinned: true},
			want: 0,
		},
		{
			name: "should report nothing when there is no configuration",
			cfg:  nil,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pinnedCAFingerprints(tt.cfg)
			if len(got) != tt.want {
				t.Fatalf("pinnedCAFingerprints returned %d fingerprints, want %d: %v", len(got), tt.want, got)
			}
		})
	}
}

// The OpenSSH form, which is what pam_ssoossh reports and what an operator
// reads off `ssh-keygen -l`.
func TestPinnedCAFingerprints_ShouldUseTheOpenSSHForm(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		CAPubkey:       []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example"},
		CAPubkeyPinned: true,
	}

	got := pinnedCAFingerprints(cfg)
	if len(got) != 1 {
		t.Fatalf("got %d fingerprints, want 1", len(got))
	}
	if !strings.HasPrefix(got[0], "SHA256:") {
		t.Errorf("fingerprint = %q, want the SHA256: form", got[0])
	}
}
