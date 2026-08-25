package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/internal/fileperm"
)

// FileAgent implements the Agent interface using SSH key files on disk:
// privKey holds the private key, privKey+".pub" the public key, and
// privKey+"-cert.pub" the signed certificate, mirroring the OpenSSH
// convention. It manages a single identity per instance.
type FileAgent struct {
	keypair *keypair.SSHKeypair
	privKey string
	cas     []ssh.PublicKey

	// HasPrivKey, HasPubKey, and HasCert record whether each corresponding
	// file existed on disk at construction time (see NewFileAgent), and are
	// updated when AddKeypair writes new ones.
	HasPrivKey bool
	HasPubKey  bool
	HasCert    bool
}

// NewFileAgent creates a FileAgent for the given private key path.
// It does not require files to be present, but will check and record their
// existence. A relative path (including a bare filename, or "~"/"~/...") is
// resolved against the user's ~/.ssh directory; os.UserHomeDir handles
// Windows and WSL, so no platform-specific logic is needed here.
//
// An empty path is rejected outright: it would resolve to ~/.ssh itself,
// and every later write or remove would then operate on the directory —
// the classic way key files end up somewhere the user never looks.
// ResolveKeyPath turns a configured key_filename into the absolute path the
// file agent actually reads and writes. Exported so anything that reports
// on key storage — `--debug`, notably — can show the same path the agent
// uses instead of echoing the configured value back. A report that stats
// the unexpanded string calls "~/.ssh/id" missing while the agent is
// happily using it, which is worse than saying nothing.
//
// A relative path (a bare filename, or "~"/"~/...") resolves against the
// user's ~/.ssh directory; os.UserHomeDir handles Windows and WSL, so no
// platform-specific logic is needed here.
//
// An empty path is rejected outright: it would resolve to ~/.ssh itself,
// and every later write or remove would then operate on the directory —
// the classic way key files end up somewhere the user never looks.
func ResolveKeyPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("key file path is empty; set key_filename to the private key file to use")
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		return filepath.Join(homeDir, strings.TrimPrefix(path, "~")), nil
	default:
		return filepath.Join(homeDir, ".ssh", path), nil
	}
}

func NewFileAgent(path string) (Agent, error) {
	path, err := ResolveKeyPath(path)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("key file path %s is a directory, not a file", path)
	}

	f := &FileAgent{
		privKey: path,
	}

	// Check for private key file. Read/parse failures are tolerated on
	// purpose: a corrupt or unreadable key must not brick the agent, because
	// the next login's AddKeypair overwrites it and self-heals.
	if _, err := os.Stat(path); err == nil {
		f.HasPrivKey = true
		data, err := os.ReadFile(path)
		if err == nil {
			kp, err := keypair.LoadSSHKeypair(data)
			if err == nil {
				f.keypair = kp
			}
		}
	}

	// Check for public key file
	if _, err := os.Stat(path + ".pub"); err == nil {
		f.HasPubKey = true
	}

	// Check for certificate file and load it if present
	certPath := path + "-cert.pub"
	if _, err := os.Stat(certPath); err == nil {
		f.HasCert = true
		certData, err := os.ReadFile(certPath)
		if err == nil && f.keypair != nil {
			// Best-effort: an unparseable cert file just leaves the
			// keypair without a certificate loaded.
			_ = f.keypair.ParseCertificateFromString(string(certData)) //nolint:errcheck // best-effort, see comment above
		}
	}

	return f, nil
}

// Type always returns AgentTypeFile.
func (f *FileAgent) Type() string {
	return AgentTypeFile
}

// Backend reports the storage backend for this agent; always BackendFile.
func (f *FileAgent) Backend() string {
	return BackendFile
}

// List returns the identities known to the agent. When filterByCA is true,
// the keypair is only included if it's signed by one of the trusted CAs
// (see SetCA); when false, the keypair is included regardless.
//
// The identity is the certificate whenever one is loaded, not the bare
// public key. Callers rely on that: `ssh inspect` casts what List(true)
// returns to *ssh.Certificate, and `ssh login`'s pruneSuperseded compares
// it against the certificate it just installed to decide what the new one
// supersedes. Returning a bare public key broke both — inspect reported
// "not a certificate", and prune found no match for the identity it had
// just written and asked to remove it. Remove now only acts on the identity
// named to it, so that mistake would no longer cost the key files, but
// prune would still be deleting the wrong thing on any agent that does.
func (f *FileAgent) List(filterByCA bool) ([]*ssh.PublicKey, error) {
	var keys []*ssh.PublicKey
	if f.keypair == nil {
		return keys, nil
	}

	if !filterByCA {
		keys = append(keys, f.identity())
		return keys, nil
	}

	for _, ca := range f.cas {
		if f.keypair.SignedBy(ca) {
			keys = append(keys, f.identity())
			break
		}
	}
	return keys, nil
}

// identity is the keypair as an ssh.PublicKey: the certificate when one is
// loaded, the bare public key otherwise. A certificate is itself an
// ssh.PublicKey, so this narrows nothing for callers that only want the key
// material.
func (f *FileAgent) identity() *ssh.PublicKey {
	if cert := f.keypair.Certificate(); cert != nil {
		var pub ssh.PublicKey = cert
		return &pub
	}
	pub := f.keypair.Public()
	return &pub
}

// Add is not supported for FileAgent; use AddKeypair to write a keypair to disk.
func (f *FileAgent) Add(key any) error {
	return errors.New("Add not supported for FileAgent")
}

// Remove deletes the key files when key is the identity this agent holds,
// and does nothing otherwise. Honouring the argument is what keeps a caller
// that names some other identity from wiping this one: `ssh login`'s
// pruneSuperseded calls Remove for every identity it judges superseded, so
// a Remove that ignored its argument would turn any misjudgement there into
// deleted key files — which is how a login once deleted the very keys it
// had just written. RemoveAll is the explicit "remove everything" path.
//
// The comparison is against the loaded identity's exact wire form (see
// identity): a certificate matches the certificate, a bare public key
// matches the bare public key, and neither stands in for the other.
func (f *FileAgent) Remove(key ssh.PublicKey) error {
	if key == nil || f.keypair == nil {
		return nil
	}
	if !bytes.Equal(key.Marshal(), (*f.identity()).Marshal()) {
		return nil
	}
	_, err := f.RemoveAll()
	return err
}

// RemoveAll deletes the private key, public key, and certificate files on
// disk for this agent's identity path. It returns 1 if files were removed,
// or 0 if there was nothing to remove. Removal failures are reported, not
// swallowed: a logout that leaves a private key behind must not look
// successful.
func (f *FileAgent) RemoveAll() (int, error) {
	fi, err := os.Stat(f.privKey)
	if err != nil {
		return 0, nil
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("refusing to remove %s: it is a directory, not a key file", f.privKey)
	}
	err = errors.Join(
		os.Remove(f.privKey),
		removeIfPresent(f.privKey+"-cert.pub"),
		removeIfPresent(f.privKey+".pub"),
	)
	if err != nil {
		return 1, fmt.Errorf("remove key files: %w", err)
	}
	return 1, nil
}

// removeIfPresent removes path, treating "already gone" as success.
func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// CleanupAgent removes the on-disk key files if the loaded certificate is
// not time-valid or not signed by a trusted CA (see SetCA). A keypair with
// no certificate loaded is left untouched. With no CAs registered it
// refuses to run rather than judging every certificate invalid and
// deleting keys that may be perfectly good.
func (f *FileAgent) CleanupAgent() error {
	if f.keypair == nil {
		return nil
	}
	cert := f.keypair.Certificate()
	if cert == nil {
		return nil
	}
	if len(f.cas) == 0 {
		return errors.New("refusing to clean up key files: no trusted CAs registered (call SetCA first)")
	}
	if !CertificateValid(cert, f.cas) {
		_, err := f.RemoveAll()
		return err
	}
	return nil
}

// Signers returns signers for all the known keys.
func (f *FileAgent) Signers() ([]ssh.Signer, error) {
	if f.keypair == nil {
		return nil, errors.New("no keypair loaded")
	}
	signer, err := ssh.NewSignerFromKey(f.keypair.Private())
	if err != nil {
		return nil, err
	}
	return []ssh.Signer{signer}, nil
}

// Close is a no-op for FileAgent, which does not maintain a persistent connection.
func (f *FileAgent) Close() error {
	return nil
}

// Agent always returns nil for FileAgent, which has no underlying
// golang.org/x/crypto/ssh/agent.Agent connection.
func (f *FileAgent) Agent() agent.Agent {
	return nil
}

// SetCA registers one or more trusted CA public keys, in addition to any
// already registered via previous calls.
func (f *FileAgent) SetCA(cas ...string) error {
	if len(cas) == 0 {
		return errors.New("at least one CA public key string is required")
	}
	parsed, err := parseCAPublicKeys(cas)
	if err != nil {
		return err
	}
	f.cas = append(f.cas, parsed...)
	return nil
}

// Certificates returns the ssh.Certificate from the file privKey+"-cert.pub" if it is signed by any trusted CA.
func (f *FileAgent) Certificates() ([]*ssh.Certificate, error) {
	if len(f.cas) == 0 {
		return nil, errors.New("CA public key is not set")
	}
	certPath := f.privKey + "-cert.pub"
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read certificate file %s: %w", certPath, err)
	}
	cert, err := keypair.ParseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("certificate file %s: %w", certPath, err)
	}
	if !CertificateValid(cert, f.cas) {
		return nil, fmt.Errorf("no valid certificates found in %s", certPath)
	}
	return []*ssh.Certificate{cert}, nil
}

// AddKeypair writes kp's private key to the agent's path, the SSH public
// key to privKey+".pub", and the certificate (if present) to
// privKey+"-cert.pub", creating the parent directory when missing. Every
// write is verified by statting the file afterwards, and every failure
// names the path involved — key files silently landing in the wrong place
// (or nowhere) is exactly the failure mode this agent exists to avoid.
// When kp carries no certificate, a stale privKey+"-cert.pub" from an
// earlier keypair is removed rather than left to mismatch the new key.
func (f *FileAgent) AddKeypair(kp *keypair.SSHKeypair) error {
	dir := filepath.Dir(f.privKey)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create key directory %s: %w", dir, err)
	}

	privPEM, err := kp.MarshalPrivateKey()
	if err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}
	if err := writeAndVerify(f.privKey, privPEM, 0o600); err != nil {
		return fmt.Errorf("private key: %w", err)
	}
	f.HasPrivKey = true

	pubStr, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}
	if err := writeAndVerify(f.privKey+".pub", []byte(pubStr), 0o644); err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	f.HasPubKey = true

	certPath := f.privKey + "-cert.pub"
	if certData := kp.MarshalCertificate(); certData != nil {
		if err := writeAndVerify(certPath, certData, 0o644); err != nil {
			return fmt.Errorf("certificate: %w", err)
		}
		f.HasCert = true
	} else {
		if err := removeIfPresent(certPath); err != nil {
			return fmt.Errorf("remove stale certificate %s: %w", certPath, err)
		}
		f.HasCert = false
	}

	f.keypair = kp
	return nil
}

// writeAndVerify writes data to path and then confirms the file actually
// exists and is non-empty, so a write that an overlay, sync tool, or
// antivirus quietly discarded surfaces as an error instead of a key that
// "disappeared".
func writeAndVerify(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// os.WriteFile applies perm only when it creates the file, so a key
	// rewritten over one left too open would keep the old mode; and on
	// Windows the mode is not access control at all. Restrict settles both.
	if err := fileperm.Restrict(path, perm); err != nil {
		return fmt.Errorf("protect %s: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("wrote %s but cannot find it afterwards: %w", path, err)
	}
	if fi.Size() == 0 && len(data) > 0 {
		return fmt.Errorf("wrote %s but the file is empty afterwards", path)
	}
	return nil
}
