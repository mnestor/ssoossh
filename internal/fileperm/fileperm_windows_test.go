//go:build windows

package fileperm

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// daclOf reads path's discretionary access list back, along with whether it
// is protected from the parent directory's inheritance.
func daclOf(t *testing.T, path string) (entries []string, protected bool) {
	t.Helper()

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the security descriptor: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("read the access list: %v", err)
	}
	if dacl == nil {
		t.Fatal("the file has no access list, which grants everyone everything")
	}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("read access list entry %d: %v", i, err)
		}
		// The SID is laid out immediately after the fixed part of the ACE;
		// SidStart is the first of its variable-length bytes. This is the
		// documented way to reach it, and why x/sys exports the struct.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		entries = append(entries, sid.String())
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("read the security descriptor control bits: %v", err)
	}
	return entries, control&windows.SE_DACL_PROTECTED != 0
}

// should hand a private key an access list naming only the process account,
// LocalSystem and Administrators, detached from whatever the containing
// directory hands down. This is the assertion the mode cannot make on
// Windows: os.Chmod there writes the read-only attribute and nothing that
// keeps another account out.
func TestRestrict_ShouldGiveAnOwnerOnlyFileAnExplicitAccessList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ssoossh")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := Restrict(path, 0o600); err != nil {
		t.Fatalf("Restrict() error = %v", err)
	}

	entries, protected := daclOf(t, path)
	if !protected {
		t.Error("the access list still inherits from the parent directory, so it takes nothing away")
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("look up the current user: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("look up LocalSystem: %v", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatalf("look up Administrators: %v", err)
	}

	want := map[string]bool{
		user.User.Sid.String():  true,
		system.String():         true,
		administrators.String(): true,
	}
	if len(entries) != len(want) {
		t.Fatalf("access list has %d entries (%v), want exactly %d", len(entries), entries, len(want))
	}
	for _, got := range entries {
		if !want[got] {
			t.Errorf("access list grants %s, which is none of the three trustees it should name", got)
		}
	}
}

// A public key or a certificate is meant to be readable, so Restrict must
// leave its access list alone rather than protecting every file it touches.
func TestRestrict_ShouldLeaveAReadableFileInheriting(t *testing.T) {
	// Created 0600 and restricted to 0644: the mode asked of Restrict is
	// what decides whether it writes an access list, and starting from the
	// tighter mode keeps the fixture from being the thing under test.
	path := filepath.Join(t.TempDir(), "id_ssoossh.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAA"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := Restrict(path, 0o644); err != nil {
		t.Fatalf("Restrict() error = %v", err)
	}

	if _, protected := daclOf(t, path); protected {
		t.Error("a 0644 file was given a protected access list; only owner-only modes should get one")
	}
}
