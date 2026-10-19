package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mnestor/ssoossh/internal/posixname"
)

// Override is one user's host override: each field set replaces the
// server's value for that user on this host only.
type Override struct {
	// UID replaces the uid. It can never go below min_id.
	UID *uint32 `yaml:"uid,omitempty"`
	// GID replaces the primary gid.
	GID *uint32 `yaml:"gid,omitempty"`
	// Home replaces the home directory.
	Home string `yaml:"home,omitempty"`
	// Shell replaces the directory's shell.
	Shell string `yaml:"shell,omitempty"`
}

// IsZero reports whether the override sets nothing.
func (o Override) IsZero() bool {
	return o.UID == nil && o.GID == nil && o.Home == "" && o.Shell == ""
}

// Overrides maps a user name, matched byte for byte, to its override. The
// file has the same shape as principals.yaml: one top-level key per
// account.
type Overrides map[string]Override

// overridesHeader is written at the top of the file by WriteOverrides.
const overridesHeader = "# ssoossh-nss host overrides. Managed by `ssoossh-nss override`;\n" +
	"# hand edits are kept but comments are not.\n"

// LoadOverrides reads the override file at path. A missing file is no
// overrides, not an error. The file need not be secret, but nobody but
// root may be able to change it, since it decides which uid a name gets:
// it must not be group- or world-writable and, for a root process, must be
// owned by root.
func LoadOverrides(path string) (Overrides, error) {
	return loadOverrides(path, os.Geteuid())
}

// loadOverrides is LoadOverrides with the effective uid supplied.
func loadOverrides(path string, euid int) (Overrides, error) {
	data, err := readCheckedFile(path, euid, 0o022, "group- or world-writable")
	if errors.Is(err, os.ErrNotExist) {
		return Overrides{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseOverrides(data)
}

// ParseOverrides decodes and validates override file contents. Unknown
// fields are an error, as in nss.yaml.
func ParseOverrides(data []byte) (Overrides, error) {
	out := Overrides{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF is a file of comments only, which is no overrides.
	if err := dec.Decode(&out); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse overrides: %w", err)
	}
	for name, o := range out {
		if err := o.validate(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// validate checks the name and each path the override sets. The min_id
// floor is the policy's to apply, since it knows the configured range.
func (o Override) validate(name string) error {
	if err := posixname.Valid(name); err != nil {
		return fmt.Errorf("override: %w", err)
	}
	if o.Home != "" && !ValidPath(o.Home) {
		return fmt.Errorf("override %q: home %q must be an absolute path", name, o.Home)
	}
	if o.Shell != "" && !ValidPath(o.Shell) {
		return fmt.Errorf("override %q: shell %q must be an absolute path", name, o.Shell)
	}
	return nil
}

// WriteOverrides replaces the override file at path with overrides,
// atomically: it writes a temporary file beside it, syncs it and renames
// it over the old one, so a reader sees the old file or the new one and
// never half of either. The file is 0644 and root's, readable by anyone
// and writable by root only.
func WriteOverrides(path string, overrides Overrides) error {
	for name, o := range overrides {
		if err := o.validate(name); err != nil {
			return err
		}
	}
	body, err := yaml.Marshal(overrides)
	if err != nil {
		// not covered: Overrides is a map of plain structs, which always
		// marshals.
		return fmt.Errorf("encode overrides: %w", err)
	}
	if len(overrides) == 0 {
		body = nil
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nss-overrides-*.yaml")
	if err != nil {
		return fmt.Errorf("write overrides: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // a no-op once renamed

	if err := writeAndSync(tmp, append([]byte(overridesHeader), body...)); err != nil {
		return fmt.Errorf("write overrides: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write overrides: %w", err)
	}
	return syncDir(dir)
}

// writeAndSync writes data to f, sets it to 0644, syncs and closes it.
func writeAndSync(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		// not covered: a write to a freshly created temporary file fails
		// only on a full or failing disk.
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		// not covered: chmod of a file this process just created cannot
		// be refused.
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		// not covered: fsync fails only on an I/O error.
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir fsyncs a directory so a rename into it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		// not covered: the directory was just written into.
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		// not covered: fsync of a directory fails only on an I/O error.
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
