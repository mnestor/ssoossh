//go:build !unix

package config

import "os"

// fileOwnerUID reports no owner. not covered: ssoosshd-nss runs only on
// Linux; this file exists so the module still builds for Windows.
func fileOwnerUID(os.FileInfo) (uint32, bool) { return 0, false }

// fileDevIno reports no device or inode. not covered: as fileOwnerUID.
func fileDevIno(os.FileInfo) (dev, ino uint64, ok bool) { return 0, 0, false }
