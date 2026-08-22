package fipsmode

import "testing"

func TestDefaultSSHKeyType(t *testing.T) {
	t.Parallel()

	if got := DefaultSSHKeyType(); got != SSHKeyTypeECDSA {
		t.Errorf("DefaultSSHKeyType() = %q, want %q", got, SSHKeyTypeECDSA)
	}
}

func TestDefaultSizeForAlgorithm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   SSHKeyType
		want int
	}{
		{name: "should return 384 for ecdsa", in: SSHKeyTypeECDSA, want: 384},
		{name: "should return 3072 for rsa", in: SSHKeyTypeRSA, want: 3072},
		{name: "should return 0 for ed25519", in: SSHKeyTypeEd25519, want: 0},
		{name: "should return 0 for an unrecognized type", in: SSHKeyType("bogus"), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := DefaultSizeForAlgorithm(tt.in); got != tt.want {
				t.Errorf("DefaultSizeForAlgorithm(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsApprovedInFIPS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   SSHKeyType
		want bool
	}{
		{name: "should approve ecdsa", in: SSHKeyTypeECDSA, want: true},
		{name: "should approve rsa", in: SSHKeyTypeRSA, want: true},
		{name: "should reject ed25519", in: SSHKeyTypeEd25519, want: false},
		{name: "should reject an empty type", in: SSHKeyType(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsApprovedInFIPS(tt.in); got != tt.want {
				t.Errorf("IsApprovedInFIPS(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidECDSASizes(t *testing.T) {
	t.Parallel()

	want := []int{256, 384, 521}
	got := ValidECDSASizes()
	if len(got) != len(want) {
		t.Fatalf("ValidECDSASizes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ValidECDSASizes()[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestMinRSASize(t *testing.T) {
	t.Parallel()

	if got := MinRSASize(); got != 2048 {
		t.Errorf("MinRSASize() = %d, want 2048", got)
	}
}

func TestFromSSHAlgorithm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		algo   string
		want   SSHKeyType
		wantOK bool
	}{
		{name: "should map ssh-ed25519", algo: "ssh-ed25519", want: SSHKeyTypeEd25519, wantOK: true},
		{name: "should map ssh-rsa", algo: "ssh-rsa", want: SSHKeyTypeRSA, wantOK: true},
		{name: "should map ecdsa-sha2-nistp256", algo: "ecdsa-sha2-nistp256", want: SSHKeyTypeECDSA, wantOK: true},
		{name: "should map ecdsa-sha2-nistp384", algo: "ecdsa-sha2-nistp384", want: SSHKeyTypeECDSA, wantOK: true},
		{name: "should map ecdsa-sha2-nistp521", algo: "ecdsa-sha2-nistp521", want: SSHKeyTypeECDSA, wantOK: true},
		{name: "should reject an unrecognized algorithm", algo: "ssh-dss", want: "", wantOK: false},
		{name: "should reject an empty algorithm", algo: "", want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FromSSHAlgorithm(tt.algo)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("FromSSHAlgorithm(%q) = (%q, %v), want (%q, %v)", tt.algo, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
