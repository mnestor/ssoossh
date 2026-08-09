package agent

import (
	"bytes"
	"errors"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
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
	// file existed on disk at construction time (see NewFileAgent).
	HasPrivKey bool
	HasPubKey  bool
	HasCert    bool
}

// NewFileAgent creates a FileAgent for the given private key path.
// It does not require files to be present, but will check and record their existence.
func NewFileAgent(path string) (Agent, error) {
	if path[:2] == "~/" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = homeDir + path[1:]
	}

	ag := &FileAgent{
		privKey: path,
	}

	// Check for private key file
	if _, err := os.Stat(path); err == nil {
		ag.HasPrivKey = true
		data, err := os.ReadFile(path)
		if err == nil {
			kp, err := keypair.LoadSSHKeypair(data)
			if err == nil {
				ag.keypair = kp
			}
		}
	}

	// Check for public key file
	pubPath := path + ".pub"
	if _, err := os.Stat(pubPath); err == nil {
		ag.HasPubKey = true
	}

	// Check for certificate file and load it if present
	certPath := path + "-cert.pub"
	if _, err := os.Stat(certPath); err == nil {
		ag.HasCert = true
		certData, err := os.ReadFile(certPath)
		if err == nil && ag.keypair != nil {
			// Best-effort: an unparseable cert file just leaves the
			// keypair without a certificate loaded.
			_ = ag.keypair.ParseCertificateFromString(string(certData)) //nolint:errcheck // best-effort, see comment above
		}
	}

	return ag, nil
}

// Type always returns AgentTypeFile.
func (a *FileAgent) Type() string {
	return AgentTypeFile
}

// Backend reports the storage backend for this agent; always BackendFile.
func (a *FileAgent) Backend() string {
	return BackendFile
}

// List returns the identities known to the agent. When filterByCA is true,
// the keypair is only included if it's signed by one of the trusted CAs
// (see SetCA); when false, the keypair is included regardless.
func (f *FileAgent) List(filterByCA bool) ([]*ssh.PublicKey, error) {
	var keys []*ssh.PublicKey
	if f.keypair == nil {
		return keys, nil
	}

	if !filterByCA {
		pub := f.keypair.Public()
		keys = append(keys, &pub)
		return keys, nil
	}

	for _, ca := range f.cas {
		if f.keypair.SignedBy(ca) {
			pub := f.keypair.Public()
			keys = append(keys, &pub)
			break
		}
	}
	return keys, nil
}

// Sign has the agent sign the data using the first key.
// func (f *FileAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
// 	if f.keypair == nil {
// 		return nil, errors.New("no keypair loaded")
// 	}
// 	pub, err := keypair.SSHPublicKey(f.keypair)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if publicKeysEqual(pub, key) {
// 		signer, err := ssh.NewSignerFromKey(f.keypair)
// 		if err != nil {
// 			return nil, err
// 		}
// 		return signer.Sign(nil, data)
// 	}
// 	return nil, errors.New("key not found")
// }

// Add is not supported for FileAgent; use AddKeypair to write a keypair to disk.
func (f *FileAgent) Add(key any) error {
	return errors.New("Add not supported for FileAgent")
}

// Remove deletes the key files for this agent's identity, regardless of the
// key passed in; a FileAgent only ever manages a single identity, so this is
// equivalent to RemoveAll.
func (f *FileAgent) Remove(key ssh.PublicKey) error {
	_, err := f.RemoveAll()
	return err
}

// RemoveAll deletes the private key, public key, and certificate files on
// disk for this agent's identity path. It returns 1 if files were removed,
// or 0 if there was nothing to remove.
func (f *FileAgent) RemoveAll() (int, error) {
	// privkey is ours so we just remove it
	if _, err := os.Stat(f.privKey); err != nil {
		return 0, nil
	}
	os.Remove(f.privKey)
	os.Remove(f.privKey + "-cert.pub")
	os.Remove(f.privKey + ".pub")
	return 1, nil
}

// CleanupAgent removes the on-disk key files if the loaded certificate is
// not time-valid or not signed by a trusted CA (see SetCA).
func (a *FileAgent) CleanupAgent() error {
	keys, err := a.List(false)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil // No keys to check
	}

	key := keys[0]

	parsed, err := ssh.ParsePublicKey((*key).Marshal())
	if err != nil {
		return nil
	}
	cert, ok := parsed.(*ssh.Certificate)
	if !ok {
		return nil
	}
	if !CertificateValid(cert, a.cas) {
		_, err := a.RemoveAll()
		return err
	}
	return nil
}

// Signers returns signers for all the known keys.
func (f *FileAgent) Signers() ([]ssh.Signer, error) {
	if f.keypair == nil {
		return nil, errors.New("no keypair loaded")
	}
	signer, err := ssh.NewSignerFromKey(f.keypair)
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
func (a *FileAgent) SetCA(cas ...string) error {
	if len(cas) == 0 {
		return errors.New("at least one CA public key string is required")
	}
	parsed := make([]ssh.PublicKey, 0, len(cas))
	for _, caStr := range cas {
		pub, err := parseCAPublicKey(caStr)
		if err != nil {
			return err
		}
		parsed = append(parsed, pub)
	}
	a.cas = append(a.cas, parsed...)
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
		return nil, errors.New("certificate file not found: " + certPath)
	}
	pub, _, _, rest, err := ssh.ParseAuthorizedKey(data)
	if err != nil || len(rest) > 0 {
		return nil, errors.New("failed to parse certificate file: " + certPath)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("file is not an ssh certificate: " + certPath)
	}
	if !CertificateValid(cert, f.cas) {
		return nil, errors.New("no valid certifiates found: " + certPath)
	}
	var certs []*ssh.Certificate
	certs = append(certs, cert)
	return certs, nil
}

// AddKeypair adds an SSHKeypair to the FileAgent's list of signers,
// writes the private key to file, the certificate (if present) to privKey+"-cert.pub",
// and the SSH public key to privKey+".pub".
func (f *FileAgent) AddKeypair(keypair *keypair.SSHKeypair) error {
	// Write private key PEM to file
	privPEM, err := keypair.MarshalPrivateKey()
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.privKey, privPEM, 0600); err != nil {
		return err
	}

	// Write public key to privKey+".pub"
	pubStr, err := keypair.MarshalAuthorizedKey()
	if err != nil {
		return err
	}
	pubPath := f.privKey + ".pub"
	if err := os.WriteFile(pubPath, []byte(pubStr), 0644); err != nil { //nolint:gosec // public key, standard OpenSSH .pub convention is world-readable; no secret material
		return err
	}

	certPath := f.privKey + "-cert.pub"
	if err := os.WriteFile(certPath, keypair.MarshalCertificate(), 0644); err != nil { //nolint:gosec // certificate, same as the .pub file above: public, no secret material
		return err
	}

	// Add to in-memory keypair and certificate
	f.keypair = keypair
	// if ed, ok := keypair.(*keypair.Ed25519KeyPair); ok {
	// 	_ = ed.ParseCertificateFromString(cert)
	// 	f.cert = ed.Certificate
	// }
	// Add support for other keypair types if needed
	return nil
}

// publicKeysEqual compares two ssh.PublicKey values for equality.
func publicKeysEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a.Marshal(), b.Marshal())
}
