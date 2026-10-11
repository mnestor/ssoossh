//go:build unix

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// Stat implements hostSystem with the numeric owner sshd's checks compare
// against root.
func (osHostSystem) Stat(path string) (fileStat, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStat{}, err
	}
	st := fileStat{mode: fi.Mode()}
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		st.uid, st.gid, st.ownerKnown = sys.Uid, sys.Gid, true
	}
	return st, nil
}

// LookupUser implements hostSystem: the account's uid and every group it
// is in, primary group included, for the mapping's read check.
func (osHostSystem) LookupUser(name string) (hostUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return hostUser{}, err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		// not covered: os/user on Unix always reports a decimal uid.
		return hostUser{}, fmt.Errorf("uid %q of %s: %w", u.Uid, name, err)
	}
	groupIDs, err := u.GroupIds()
	if err != nil {
		// not covered: needs an account whose group list cannot be read,
		// which a test cannot arrange without root.
		return hostUser{}, fmt.Errorf("groups of %s: %w", name, err)
	}

	hu := hostUser{name: name, uid: uint32(uid)}
	for _, g := range append(groupIDs, u.Gid) {
		if gid, err := strconv.ParseUint(g, 10, 32); err == nil {
			hu.gids = append(hu.gids, uint32(gid))
		}
	}
	return hu, nil
}

// Privileged implements hostSystem.
func (osHostSystem) Privileged() bool { return os.Geteuid() == 0 }

// ClockSynchronized implements hostSystem through systemd's timedatectl.
// Anywhere it is missing -- macOS, a host without systemd -- synchronization
// is reported as unknown rather than guessed.
func (osHostSystem) ClockSynchronized(ctx context.Context) (bool, bool) {
	return parseNTPSynchronized(exec.CommandContext(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value").Output())
}

// parseNTPSynchronized reads timedatectl's answer. Anything but a plain
// "yes" or "no", including timedatectl failing to run, is unknown.
func parseNTPSynchronized(out []byte, err error) (synced, known bool) {
	if err != nil {
		return false, false
	}
	switch strings.TrimSpace(string(out)) {
	case "yes":
		return true, true
	case "no":
		return false, true
	default:
		return false, false
	}
}
