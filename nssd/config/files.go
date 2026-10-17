package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrInsecureFile is a configuration or key file others could read or
// change.
var ErrInsecureFile = errors.New("insecure file permissions")

// maxConfigFileBytes bounds what is read from any one configuration file.
// nss.yaml, the key and the overrides are a few kilobytes at most.
const maxConfigFileBytes = 1 << 20

// ReadPrivateFile reads a file that must be private to root: a regular
// file with no group or other permission bits and, when this process runs
// as root, owned by root. The check is made on the opened file, so the
// file read is the file checked. The owner check is skipped when not
// running as root, so the daemon runs from a test or a developer's shell.
func ReadPrivateFile(path string) ([]byte, error) {
	return readPrivateFile(path, os.Geteuid())
}

// readPrivateFile is ReadPrivateFile with the effective uid supplied.
func readPrivateFile(path string, euid int) ([]byte, error) {
	return readCheckedFile(path, euid, 0o077, "group- or world-accessible")
}

// readCheckedFile opens path, refuses it when any bit in forbidden is set
// in its mode or, for a root process, when root does not own it, and
// returns its contents.
func readCheckedFile(path string, euid int, forbidden os.FileMode, what string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		// not covered: fstat on a descriptor just opened fails only on an
		// I/O error the kernel reports for the open file itself.
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := checkInfo(path, info, euid, forbidden, what); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileBytes+1))
	if err != nil {
		// not covered: reading a regular file that opened and stat'd
		// cleanly fails only on a device I/O error.
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxConfigFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxConfigFileBytes)
	}
	return data, nil
}

// checkInfo applies readCheckedFile's rules to a file's metadata.
func checkInfo(path string, info os.FileInfo, euid int, forbidden os.FileMode, what string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrInsecureFile, path)
	}
	if perm := info.Mode().Perm(); perm&forbidden != 0 {
		return fmt.Errorf("%w: %s is %s (mode %04o)", ErrInsecureFile, path, what, perm)
	}
	if euid == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
			return fmt.Errorf("%w: %s is owned by uid %d, not root", ErrInsecureFile, path, st.Uid)
		}
	}
	return nil
}

// Key is a fleet key.
type Key struct {
	// ID is the key id, safe to log.
	ID string
	// Secret is the key secret, never logged.
	Secret string
}

// ReadKeyFile reads the fleet key file: private to root like nss.yaml,
// line 1 the key id, line 2 the secret. Blank lines around them are
// ignored; anything else is an error, so a file with a stray third line is
// not silently half-used.
func ReadKeyFile(path string) (Key, error) {
	return readKeyFile(path, os.Geteuid())
}

// readKeyFile is ReadKeyFile with the effective uid supplied.
func readKeyFile(path string, euid int) (Key, error) {
	data, err := readPrivateFile(path, euid)
	if err != nil {
		return Key{}, err
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		// not covered: the input is at most maxConfigFileBytes, and a
		// line that long is the only way bufio.Scanner fails on a reader
		// that cannot error.
		return Key{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(lines) != 2 {
		// The count only: the file holds the secret, which no error may
		// carry.
		return Key{}, fmt.Errorf("%s must hold two lines, the key id then the secret; it holds %d", path, len(lines))
	}
	return Key{ID: lines[0], Secret: lines[1]}, nil
}

// Stamp identifies one version of a file, to notice when it changed
// without reading it: its device, inode, size and modification time. A
// replacement by rename changes the inode even within one second.
type Stamp struct {
	dev, ino uint64
	size     int64
	mtime    time.Time
	exists   bool
}

// StampOf returns path's current stamp. A missing file has a stamp of its
// own, so a file that appears or disappears counts as a change.
func StampOf(path string) (Stamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Stamp{}, nil
	}
	if err != nil {
		return Stamp{}, fmt.Errorf("stat %s: %w", path, err)
	}
	s := Stamp{size: info.Size(), mtime: info.ModTime(), exists: true}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		// Dev is uint32 on some platforms, hence the conversion.
		s.dev, s.ino = uint64(st.Dev), st.Ino
	}
	return s, nil
}

// Exists reports whether the stamped file existed.
func (s Stamp) Exists() bool { return s.exists }

// hostDomain is the host's DNS domain: what follows the first dot of the
// hostname, else the first "domain" or "search" entry in /etc/resolv.conf,
// else empty.
func hostDomain() string {
	return domainOfHost(os.Hostname, "/etc/resolv.conf")
}

// domainOfHost is hostDomain with the hostname lookup and resolv.conf path
// supplied. Either failing leaves its input empty, which domainFrom treats
// as "nothing known": discovery then needs an explicit domain.
func domainOfHost(hostname func() (string, error), resolvPath string) string {
	name, err := hostname()
	if err != nil {
		name = ""
	}
	resolv, err := os.ReadFile(resolvPath)
	if err != nil {
		resolv = nil
	}
	return domainFrom(name, resolv)
}

// domainFrom is hostDomain over a hostname and resolv.conf contents.
func domainFrom(hostname string, resolvConf []byte) string {
	if _, domain, ok := strings.Cut(hostname, "."); ok && domain != "" {
		return strings.TrimSuffix(domain, ".")
	}
	scanner := bufio.NewScanner(bytes.NewReader(resolvConf))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && (fields[0] == "domain" || fields[0] == "search") {
			return strings.TrimSuffix(fields[1], ".")
		}
	}
	return ""
}
