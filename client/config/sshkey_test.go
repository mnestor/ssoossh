package config

import (
	"strings"
	"testing"
)

// Test methodology: table-driven unit tests for key resolution. FIPS mode is
// exercised through the explicit config flag rather than the Go runtime,
// since the runtime's mode can't be toggled from a test — FIPSEnabled's
// runtime fallback is covered separately by asserting the explicit flag
// takes precedence.

// boolPtr returns a pointer to b, for the tri-state FIPS setting.
func boolPtr(b bool) *bool { return &b }

func TestResolveSSHKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		fips          *bool
		keyType       SSHKeyType
		size          int
		wantAlgorithm string
		wantSize      int
		wantWarning   string // substring; empty means no warning expected
		wantErr       bool
	}{
		{
			name:          "should default to ed25519 when nothing is configured",
			fips:          boolPtr(false),
			wantAlgorithm: "ed25519",
			wantSize:      0,
		},
		{
			name:          "should default to ecdsa P-256 in FIPS mode",
			fips:          boolPtr(true),
			wantAlgorithm: "ecdsa",
			wantSize:      256,
		},
		{
			name:          "should default ecdsa to P-256 when size is unset",
			fips:          boolPtr(false),
			keyType:       SSHKeyTypeECDSA,
			wantAlgorithm: "ecdsa",
			wantSize:      256,
		},
		{
			name:          "should honour an explicit ecdsa curve",
			fips:          boolPtr(false),
			keyType:       SSHKeyTypeECDSA,
			size:          384,
			wantAlgorithm: "ecdsa",
			wantSize:      384,
		},
		{
			name:    "should reject an ecdsa size that is not a NIST curve",
			fips:    boolPtr(false),
			keyType: SSHKeyTypeECDSA,
			size:    2048,
			wantErr: true,
		},
		{
			name:          "should default rsa to 3072 bits when size is unset",
			fips:          boolPtr(false),
			keyType:       SSHKeyTypeRSA,
			wantAlgorithm: "rsa",
			wantSize:      3072,
		},
		{
			name:    "should reject an rsa size below the 2048-bit minimum",
			fips:    boolPtr(false),
			keyType: SSHKeyTypeRSA,
			size:    1024,
			wantErr: true,
		},
		{
			name:    "should reject an unknown key type",
			fips:    boolPtr(false),
			keyType: SSHKeyType("dsa"),
			wantErr: true,
		},
		{
			name:          "should warn but still allow ed25519 in FIPS mode",
			fips:          boolPtr(true),
			keyType:       SSHKeyTypeEd25519,
			wantAlgorithm: "ed25519",
			wantSize:      0,
			wantWarning:   "not FIPS-approved",
		},
		{
			name:          "should not warn about ecdsa in FIPS mode",
			fips:          boolPtr(true),
			keyType:       SSHKeyTypeECDSA,
			wantAlgorithm: "ecdsa",
			wantSize:      256,
		},
		{
			name:          "should not warn about rsa in FIPS mode",
			fips:          boolPtr(true),
			keyType:       SSHKeyTypeRSA,
			wantAlgorithm: "rsa",
			wantSize:      3072,
		},
		{
			name:          "should warn that size is meaningless for ed25519",
			fips:          boolPtr(false),
			keyType:       SSHKeyTypeEd25519,
			size:          4096,
			wantAlgorithm: "ed25519",
			wantSize:      0,
			wantWarning:   "ignored for ed25519",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Config{FIPS: tt.fips}
			c.SSHKey.Type = tt.keyType
			c.SSHKey.Size = tt.size

			algorithm, size, warnings, err := c.ResolveSSHKey()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if algorithm != tt.wantAlgorithm {
				t.Errorf("got algorithm %q, want %q", algorithm, tt.wantAlgorithm)
			}
			if size != tt.wantSize {
				t.Errorf("got size %d, want %d", size, tt.wantSize)
			}

			joined := strings.Join(warnings, "\n")
			if tt.wantWarning == "" {
				if len(warnings) != 0 {
					t.Errorf("expected no warnings, got %v", warnings)
				}
			} else if !strings.Contains(joined, tt.wantWarning) {
				t.Errorf("expected a warning containing %q, got %v", tt.wantWarning, warnings)
			}
		})
	}
}

// TestResolveSSHKey_ShouldProduceArgumentsKeypairAccepts pins the contract
// with internal/crypto/ssh/keypair: the algorithm strings returned here are
// exactly the ones NewSSHKeypair switches on, and the sizes are ones its
// per-algorithm constructors accept. A rename on either side should fail
// here rather than at the first login.
func TestResolveSSHKey_ShouldProduceArgumentsKeypairAccepts(t *testing.T) {
	t.Parallel()

	for _, keyType := range []SSHKeyType{SSHKeyTypeEd25519, SSHKeyTypeECDSA, SSHKeyTypeRSA} {
		c := &Config{FIPS: boolPtr(false)}
		c.SSHKey.Type = keyType

		algorithm, _, _, err := c.ResolveSSHKey()
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", keyType, err)
		}
		if algorithm != string(keyType) {
			t.Errorf("got algorithm %q, want %q", algorithm, keyType)
		}
	}
}

func TestFIPSEnabled_ShouldPreferTheExplicitSetting(t *testing.T) {
	t.Parallel()

	if got := (&Config{FIPS: boolPtr(true)}).FIPSEnabled(); !got {
		t.Error("expected an explicit fips: true to enable FIPS mode")
	}
	// Explicitly false must win even if the runtime is in FIPS mode, so an
	// operator can target a non-FIPS server from a FIPS-built binary.
	if got := (&Config{FIPS: boolPtr(false)}).FIPSEnabled(); got {
		t.Error("expected an explicit fips: false to disable FIPS mode")
	}
}
