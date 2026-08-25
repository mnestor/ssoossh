// Package fileperm gives a file the protection its Unix mode asks for, in
// whatever terms the running platform actually enforces.
//
// It exists because os.Chmod means two very different things. On Unix it is
// the whole story: 0600 is what ssh, sshd and every other tool check before
// they will touch a private key. On Windows it writes one bit, the read-only
// attribute, and nothing else -- a key written with 0600 there is readable
// by every account on the machine. Every place this repo writes a secret to
// disk goes through Restrict so that difference lives in one file instead of
// being re-discovered at each write site.
package fileperm

import "os"

// Restrict applies perm to path and, when perm denies group and other, makes
// that denial real on platforms where the mode alone does not.
//
// It always chmods, even for a file that already exists: os.WriteFile only
// applies its mode when it creates the file, so rewriting a key that was
// somehow left world-readable would otherwise leave it that way.
func Restrict(path string, perm os.FileMode) error {
	// Returned as-is: os.Chmod fails with an *fs.PathError, which already
	// reads "chmod <path>: <reason>". Wrapping it here only says chmod twice.
	if err := os.Chmod(path, perm); err != nil {
		return err
	}

	// A mode with no group or other bits is the caller saying "owner only".
	// Anything more open is a public key or a certificate, which is meant to
	// be readable and needs no access list of its own.
	if perm&0o077 != 0 {
		return nil
	}
	return restrictToOwner(path)
}
