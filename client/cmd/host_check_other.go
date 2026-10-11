//go:build !unix

package cmd

import (
	"context"
	"os"
	"os/user"
)

// Stat implements hostSystem without an owner: Windows files have ACLs,
// not the uid and mode bits sshd's checks are written for, so every
// ownership check is skipped there rather than guessed.
//
// not covered: built only on Windows; the unit tests run on Linux.
func (osHostSystem) Stat(path string) (fileStat, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStat{}, err
	}
	return fileStat{mode: fi.Mode()}, nil
}

// LookupUser implements hostSystem: it confirms the account exists. The
// uid and groups it would need for a read check do not exist here.
//
// not covered: built only on Windows; the unit tests run on Linux.
func (osHostSystem) LookupUser(name string) (hostUser, error) {
	if _, err := user.Lookup(name); err != nil {
		return hostUser{}, err
	}
	return hostUser{name: name}, nil
}

// Privileged implements hostSystem. There is no euid to ask, so this
// claims privilege and leaves sshd's own error to say what it lacked.
//
// not covered: built only on Windows; the unit tests run on Linux.
func (osHostSystem) Privileged() bool { return true }

// ClockSynchronized implements hostSystem: unknown, since there is no
// timedatectl to ask.
//
// not covered: built only on Windows; the unit tests run on Linux.
func (osHostSystem) ClockSynchronized(context.Context) (bool, bool) { return false, false }
