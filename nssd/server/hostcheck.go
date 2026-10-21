package server

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultNSSwitchPath is glibc's nsswitch.conf.
const DefaultNSSwitchPath = "/etc/nsswitch.conf"

// NSSwitchLine is one database's line in nsswitch.conf.
type NSSwitchLine struct {
	// Database is passwd, group or shadow.
	Database string `json:"database"`
	// Value is the line's sources, as written; empty when the line is
	// missing.
	Value string `json:"value"`
	// Present is false when nsswitch.conf has no line for the database.
	Present bool `json:"present"`
	// Systemd is true when the token systemd itself is listed. compat,
	// which Leap 15.6 ships, does not imply it.
	Systemd bool `json:"systemd"`
	// Ssoossh is true when the token ssoossh is listed: the libnss_ssoossh
	// module (github.com/mnestor/ssoossh-libnss), the userdb client for the
	// RHEL 8 family, where the stock nss-systemd is too old to speak userdb.
	// Either token names a client that consults this daemon.
	Ssoossh bool `json:"ssoossh"`
}

// NSSwitchReport is the nsswitch.conf check (README U2).
type NSSwitchReport struct {
	// Path is the file read.
	Path string `json:"path"`
	// Lines are passwd, group and shadow, in that order.
	Lines []NSSwitchLine `json:"lines"`
	// Error is set when the file could not be read.
	Error string `json:"error,omitempty"`
}

// consulted reports whether the line names a userdb client that queries this
// daemon: systemd (nss-systemd, 245+) or ssoossh (the RHEL 8 module).
func (l NSSwitchLine) consulted() bool {
	return l.Systemd || l.Ssoossh
}

// OK reports whether every line names a userdb client (systemd or ssoossh).
func (r NSSwitchReport) OK() bool {
	return r.Error == "" && !slices.ContainsFunc(r.Lines, func(l NSSwitchLine) bool { return !l.consulted() })
}

// Problems describes each failing line, for the log and for status.
func (r NSSwitchReport) Problems() []string {
	if r.Error != "" {
		return []string{fmt.Sprintf("cannot read %s: %s", r.Path, r.Error)}
	}
	var out []string
	for _, l := range r.Lines {
		switch {
		case !l.Present:
			out = append(out, fmt.Sprintf("%s has no %s: line, so neither systemd nor ssoossh is consulted for %s", r.Path, l.Database, l.Database))
		case !l.consulted():
			out = append(out, fmt.Sprintf("the %s: line %q in %s lists neither systemd nor ssoossh", l.Database, l.Value, r.Path))
		}
	}
	return out
}

// CheckNSSwitch reads nsswitch.conf at path and reports, for passwd, group
// and shadow, whether a userdb client (the systemd or the ssoossh source) is
// listed. A missing entry would otherwise look like missing users.
func CheckNSSwitch(path string) NSSwitchReport {
	r := NSSwitchReport{Path: path}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		r.Error = err.Error()
		return r
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		db, sources, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[strings.TrimSpace(db)] = strings.TrimSpace(sources)
	}
	// passwd and group for the records; shadow because without it sshd
	// refuses every directory user under UsePAM yes and admits a locked one
	// under UsePAM no (measured on AlmaLinux 9, which ships "shadow:
	// files").
	for _, db := range []string{"passwd", "group", "shadow"} {
		value, present := values[db]
		r.Lines = append(r.Lines, NSSwitchLine{
			Database: db,
			Value:    value,
			Present:  present,
			Systemd:  slices.Contains(strings.Fields(value), "systemd"),
			Ssoossh:  slices.Contains(strings.Fields(value), "ssoossh"),
		})
	}
	return r
}

// RunningCaches returns the names of the NSS caching daemons running,
// nscd or unscd, found by their comm under proc. Either would cache a
// "not found" the daemon answered while the directory was slow (README
// U5, R26). A warning only, so the comm, which any process can set, is
// good enough.
func RunningCaches(proc string) []string {
	matches, err := filepath.Glob(filepath.Join(proc, "[0-9]*", "comm"))
	if err != nil {
		// not covered: the pattern is constant and well formed.
		return nil
	}
	var found []string
	for _, m := range matches {
		comm, err := os.ReadFile(filepath.Clean(m))
		if err != nil {
			continue // the process exited
		}
		name := strings.TrimSpace(string(comm))
		if (name == "nscd" || name == "unscd") && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	return found
}
