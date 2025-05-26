package agent

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"strings"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// FileAgent implements the agent.Agent interface using SSH key files on disk.
type FileAgent struct {
	keypair *keypair.SshKeypair
	// cert       *ssh.Certificate
	privKey    string
	ca         ssh.PublicKey
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
			kp, err := keypair.LoadSshKeypair(data)
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
			// Try to parse and set the certificate on the keypair
			ag.keypair.ParseCertificateFromString(string(certData))
			// if ed, ok := *ag.keypair.(*keypair.Ed25519KeyPair); ok {
			// 	_ = ed.ParseCertificateFromString(string(certData))
			// 	ag.cert = ed.Certificate()
			// }
			// Add support for other keypair types if needed
		}
	}

	return ag, nil
}

func (a *FileAgent) Type() string {
	return AgentTypeFile
}

// List returns the identities known to the agent.
func (f *FileAgent) List() ([]*ssh.PublicKey, error) {
	var keys []*ssh.PublicKey
	if f.keypair == nil {
		return keys, nil
	}

	if f.keypair.SignedBy(f.ca) {
		pub := f.keypair.Public()
		keys = append(keys, &pub)
	}
	// // Only include ssh.Certificate keys signed by the CA if CA is set
	// if f.keypair.Certificate() != nil && f.ca != nil {
	// 	if publicKeysEqual(f.cert.SignatureKey, f.ca) {
	// 		certPub := ssh.PublicKey(f.cert)
	// 		keys = append(keys, &certPub)
	// 	}
	// } else {
	// 	keys = append(keys, &pub)
	// }
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

// Add is not supported for FileAgent.
func (f *FileAgent) Add(key interface{}) error {
	return errors.New("Add not supported for FileAgent")
}

// Remove is not supported for FileAgent.
func (f *FileAgent) Remove(key ssh.PublicKey) error {
	_, _ = f.RemoveAll()
	return nil
}

// RemoveAll is not supported for FileAgent.
func (f *FileAgent) RemoveAll() (int, error) {
	// privkey is ours so we just remove it
	if x, _ := os.Stat(f.privKey); x == nil {
		return 0, nil
	}
	os.Remove(f.privKey)
	os.Remove(f.privKey + "-cert.pub")
	os.Remove(f.privKey + ".pub")
	return 1, nil
}

func (a *FileAgent) CleanupAgent() error {
	keys, err := a.List()
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
	if !CertificateValid(cert, a.ca) {
		a.RemoveAll()
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

func (f *FileAgent) Close() error {
	// FileAgent does not maintain a persistent connection, so nothing to close.
	return nil
}

func (f *FileAgent) Agent() agent.Agent {
	return nil
}

func (a *FileAgent) SetCA(caStr string) error {
	if caStr == "" {
		return errors.New("CA public key string cannot be empty")
	}
	caStr = strings.TrimSpace(caStr)
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(caStr))
	if err != nil || len(rest) > 0 {
		// Try parsing as raw base64
		data, err2 := base64.StdEncoding.DecodeString(caStr)
		if err2 != nil {
			return errors.New("failed to parse CA public key string")
		}
		pub, err = ssh.ParsePublicKey(data)
		if err != nil {
			return errors.New("failed to parse CA public key from base64")
		}
	}
	a.ca = pub
	return nil
}

// Certificates returns the ssh.Certificate from the file privKey+"-cert.pub" if it is signed by the CA.
func (f *FileAgent) Certificates() ([]*ssh.Certificate, error) {
	if f.ca == nil {
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
	if !CertificateValid(cert, f.ca) {
		return nil, errors.New("no valid certifiates found: " + certPath)
	}
	var certs []*ssh.Certificate
	certs = append(certs, cert)
	return certs, nil
}

// AddKeypair adds an SshKeypair to the FileAgent's list of signers,
// writes the private key to file, the certificate (if present) to privKey+"-cert.pub",
// and the SSH public key to privKey+".pub".
func (f *FileAgent) AddKeypair(keypair *keypair.SshKeypair) error {
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
	if err := os.WriteFile(pubPath, []byte(pubStr), 0644); err != nil {
		return err
	}

	certPath := f.privKey + "-cert.pub"
	if err := os.WriteFile(certPath, keypair.MarshalCertificate(), 0644); err != nil {
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
