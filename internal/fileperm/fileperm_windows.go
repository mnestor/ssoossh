//go:build windows

package fileperm

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileAllAccess is Win32's FILE_ALL_ACCESS, which golang.org/x/sys/windows
// does not export. Spelled out rather than reached for as GENERIC_ALL
// because a generic right stored in an ACE only means something once
// something maps it against the object type, and nothing on this path does.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

// restrictToOwner replaces path's access list with an explicit one naming
// three trustees -- the account running this process, LocalSystem, and the
// Administrators group -- and detaches the file from its parent directory's
// inheritance.
//
// Those three are the faithful translation of Unix 0600 rather than a
// stricter reading of it: root can read a 0600 file, and LocalSystem and
// Administrators are what root turns into here. It is also the set OpenSSH
// for Windows accepts when it checks whether a private key is adequately
// protected, so a key written this way is one ssh.exe will agree to use.
func restrictToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("look up the account running this process: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("look up the LocalSystem account: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("look up the Administrators group: %w", err)
	}

	entries := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{user.User.Sid, system, administrators} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: fileAllAccess,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}

	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build the access list for %s: %w", path, err)
	}

	// PROTECTED_DACL_SECURITY_INFORMATION is the half that does the work.
	// Without it the entries inherited from the parent directory survive
	// alongside the new ones, and under a default %USERPROFILE% or
	// %ProgramData% those already grant more than the three trustees above:
	// the new list would add nothing and take nothing away.
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("set the access list on %s: %w", path, err)
	}
	return nil
}
