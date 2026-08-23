package agent

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// should report file as the backend and agent type for a FileAgent.
func TestFileAgent_TypeAndBackend(t *testing.T) {
	t.Parallel()

	f := &FileAgent{}

	if got := f.Type(); got != AgentTypeFile {
		t.Errorf("Type() = %q, want %q", got, AgentTypeFile)
	}
	if got := f.Backend(); got != BackendFile {
		t.Errorf("Backend() = %q, want %q", got, BackendFile)
	}
}

// should create a FileAgent for a path with no existing key material.
func TestNewFileAgent_WhenNoFilesExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v, want nil", err)
	}

	fa, ok := ag.(*FileAgent)
	if !ok {
		t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
	}
	if fa.HasPrivKey || fa.HasPubKey || fa.HasCert {
		t.Errorf("expected no existing key material, got HasPrivKey=%v HasPubKey=%v HasCert=%v", fa.HasPrivKey, fa.HasPubKey, fa.HasCert)
	}
}

// should resolve a non-absolute path to an absolute one under the user's home directory.
func TestNewFileAgent_ResolvesToAbsolutePath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "should expand a leading ~/ to the home directory", path: "~/nonexistent-ssoossh-test-key", want: filepath.Join(home, "nonexistent-ssoossh-test-key")},
		{name: "should resolve a bare filename under ~/.ssh", path: "nonexistent-ssoossh-test-key", want: filepath.Join(home, ".ssh", "nonexistent-ssoossh-test-key")},
		{name: "should resolve a relative path under ~/.ssh", path: filepath.Join("sub", "nonexistent-ssoossh-test-key"), want: filepath.Join(home, ".ssh", "sub", "nonexistent-ssoossh-test-key")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ag, err := NewFileAgent(tt.path)
			if err != nil {
				t.Fatalf("NewFileAgent() error = %v", err)
			}
			fa, ok := ag.(*FileAgent)
			if !ok {
				t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
			}
			if !filepath.IsAbs(fa.privKey) {
				t.Errorf("privKey = %q, want an absolute path", fa.privKey)
			}
			if fa.privKey != tt.want {
				t.Errorf("privKey = %q, want %q", fa.privKey, tt.want)
			}
		})
	}
}

// should leave an already-absolute path untouched.
func TestNewFileAgent_AbsolutePathUnchanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v", err)
	}
	fa, ok := ag.(*FileAgent)
	if !ok {
		t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
	}
	if fa.privKey != path {
		t.Errorf("privKey = %q, want %q", fa.privKey, path)
	}
}

// should reject an empty path instead of silently resolving it to ~/.ssh
// itself, which every later write or remove would then operate on.
func TestNewFileAgent_EmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := NewFileAgent(""); err == nil {
		t.Error("NewFileAgent(\"\") error = nil, want error")
	}
	if _, err := NewFileAgent("   "); err == nil {
		t.Error("NewFileAgent(\"   \") error = nil, want error")
	}
}

// should reject a path that names an existing directory.
func TestNewFileAgent_DirectoryPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := NewFileAgent(dir); err == nil {
		t.Error("NewFileAgent() error = nil, want error for a directory path")
	}
}

// should detect and load existing private key, public key, and certificate files.
func TestNewFileAgent_LoadsExistingKeyMaterial(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	cert := newTestCert(t, kp.Public(), caSigner)

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")
	privPEM, err := kp.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("MarshalPrivateKey() error = %v", err)
	}
	if err := os.WriteFile(path, privPEM, 0600); err != nil {
		t.Fatalf("WriteFile(priv) error = %v", err)
	}
	pubStr, err := kp.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}
	if err := os.WriteFile(path+".pub", []byte(pubStr), 0644); err != nil { //nolint:gosec // test fixture, not secret material
		t.Fatalf("WriteFile(pub) error = %v", err)
	}
	if err := os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0644); err != nil { //nolint:gosec // test fixture, not secret material
		t.Fatalf("WriteFile(cert) error = %v", err)
	}

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v", err)
	}
	fa, ok := ag.(*FileAgent)
	if !ok {
		t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
	}
	if !fa.HasPrivKey || !fa.HasPubKey || !fa.HasCert {
		t.Errorf("HasPrivKey=%v HasPubKey=%v HasCert=%v, want all true", fa.HasPrivKey, fa.HasPubKey, fa.HasCert)
	}
	if fa.keypair == nil {
		t.Fatal("expected keypair to be loaded")
	}
	if fa.keypair.Certificate() == nil {
		t.Error("expected certificate to be loaded onto the keypair")
	}
}

// should still record HasCert when the certificate file exists but cannot be parsed, leaving the keypair's certificate unset.
func TestNewFileAgent_UnparseableCertFile(t *testing.T) {
	t.Parallel()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")
	privPEM, err := kp.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("MarshalPrivateKey() error = %v", err)
	}
	if err := os.WriteFile(path, privPEM, 0600); err != nil {
		t.Fatalf("WriteFile(priv) error = %v", err)
	}
	if err := os.WriteFile(path+"-cert.pub", []byte("not a certificate"), 0644); err != nil { //nolint:gosec // test fixture, not secret material
		t.Fatalf("WriteFile(cert) error = %v", err)
	}

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v", err)
	}
	fa, ok := ag.(*FileAgent)
	if !ok {
		t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
	}
	if !fa.HasCert {
		t.Error("expected HasCert to be true since the file exists, even though it is unparseable")
	}
	if fa.keypair.Certificate() != nil {
		t.Error("expected no certificate to be loaded from an unparseable cert file")
	}
}

// should write private key, public key, and certificate files when adding a keypair.
func TestFileAgent_AddKeypair_WritesFiles(t *testing.T) {
	t.Parallel()

	newKeypair := func(t *testing.T) *keypair.SSHKeypair {
		t.Helper()
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		return kp
	}

	t.Run("should write key and public key but no certificate file when the keypair has none", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		ag, err := NewFileAgent(path)
		if err != nil {
			t.Fatalf("NewFileAgent() error = %v", err)
		}

		if err := ag.AddKeypair(newKeypair(t)); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}

		for _, suffix := range []string{"", ".pub"} {
			if _, err := os.Stat(path + suffix); err != nil {
				t.Errorf("expected file %s to exist, got error: %v", path+suffix, err)
			}
		}
		if _, err := os.Stat(path + "-cert.pub"); !os.IsNotExist(err) {
			t.Errorf("expected no certificate file for a certificate-less keypair, stat error = %v", err)
		}
	})

	t.Run("should write all three files when the keypair carries a certificate", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		ag, err := NewFileAgent(path)
		if err != nil {
			t.Fatalf("NewFileAgent() error = %v", err)
		}

		ca := newKeypair(t)
		caSigner, err := ssh.NewSignerFromKey(ca.Private())
		if err != nil {
			t.Fatalf("NewSignerFromKey() error = %v", err)
		}
		kp := newKeypair(t)
		kp.SetCertificate(newTestCert(t, kp.Public(), caSigner))

		if err := ag.AddKeypair(kp); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}

		for _, suffix := range []string{"", ".pub", "-cert.pub"} {
			if _, err := os.Stat(path + suffix); err != nil {
				t.Errorf("expected file %s to exist, got error: %v", path+suffix, err)
			}
		}
	})

	t.Run("should remove a stale certificate file left by an earlier keypair", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		certPath := path + "-cert.pub"
		if err := os.WriteFile(certPath, []byte("stale"), 0600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		f := &FileAgent{privKey: path}

		if err := f.AddKeypair(newKeypair(t)); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}
		if _, err := os.Stat(certPath); !os.IsNotExist(err) {
			t.Errorf("expected stale certificate %s to be removed, stat error = %v", certPath, err)
		}
	})

	t.Run("should create a missing parent directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "deeper", "id_ssoossh")
		f := &FileAgent{privKey: path}

		if err := f.AddKeypair(newKeypair(t)); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist, got error: %v", path, err)
		}
	})
}

// should refuse to add or remove keys directly, since FileAgent is not a live agent.
func TestFileAgent_AddAndRemove_Unsupported(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f := &FileAgent{privKey: filepath.Join(dir, "id_ssoossh")}

	if err := f.Add(nil); err == nil {
		t.Error("Add() error = nil, want error for unsupported operation")
	}
}

// should return no identities when no keypair is loaded, and the keypair filtered by CA trust otherwise.
func TestFileAgent_List(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	other, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf.SetCertificate(newTestCert(t, leaf.Public(), caSigner))

	t.Run("should return no identities when no keypair is loaded", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{}
		got, err := f.List(false)
		if err != nil {
			t.Fatalf("List(false) error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List(false) returned %d identities, want 0", len(got))
		}
	})

	t.Run("should return the loaded keypair unfiltered", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{keypair: leaf}
		got, err := f.List(false)
		if err != nil {
			t.Fatalf("List(false) error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(false) returned %d identities, want 1", len(got))
		}
	})

	t.Run("should return the keypair when filtering by a CA that signed it", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{keypair: leaf, cas: []ssh.PublicKey{ca.Public()}}
		got, err := f.List(true)
		if err != nil {
			t.Fatalf("List(true) error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(true) returned %d identities, want 1", len(got))
		}
	})

	// The type matters, not just the count. ssh_inspect casts the result to
	// *ssh.Certificate on the grounds that "List(true) filters to
	// certificates", and ssh login's pruneSuperseded compares what comes
	// back against the certificate it just installed. Handing either of
	// them a bare public key silently breaks both: inspect prints "not a
	// certificate", and prune decides the identity it just wrote is a
	// superseded one and deletes it.
	t.Run("should return the certificate rather than the bare public key when filtering by CA", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{keypair: leaf, cas: []ssh.PublicKey{ca.Public()}}
		got, err := f.List(true)
		if err != nil {
			t.Fatalf("List(true) error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(true) returned %d identities, want 1", len(got))
		}
		if _, ok := (*got[0]).(*ssh.Certificate); !ok {
			t.Fatalf("List(true) returned %T, want *ssh.Certificate", *got[0])
		}
	})

	t.Run("should return nothing when filtering by a CA that did not sign it", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{keypair: leaf, cas: []ssh.PublicKey{other.Public()}}
		got, err := f.List(true)
		if err != nil {
			t.Fatalf("List(true) error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List(true) returned %d identities, want 0", len(got))
		}
	})
}

// should remove the key files only when the key passed in is the identity
// this agent holds.
//
// Honouring the argument is what keeps a caller that asks to remove some
// other identity from wiping this one. `ssh login`'s pruneSuperseded calls
// Remove for every identity it considers superseded; when it misjudges the
// current identity as superseded — as it did once already, from List
// returning a bare public key instead of the certificate — a Remove that
// ignores its argument turns that misjudgement into deleted key files.
// RemoveAll stays the explicit "remove everything" path.
func TestFileAgent_Remove(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}

	// held builds a FileAgent whose files are on disk and whose identity is
	// the returned public key: the certificate when withCert is set, the
	// bare public key otherwise.
	held := func(t *testing.T, withCert bool) (*FileAgent, ssh.PublicKey) {
		t.Helper()
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		if withCert {
			kp.SetCertificate(newTestCert(t, kp.Public(), caSigner))
		}
		f := &FileAgent{privKey: filepath.Join(t.TempDir(), "id_ssoossh")}
		if err := f.AddKeypair(kp); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}
		return f, *f.identity()
	}

	// other is an identity this agent does not hold.
	other := func(t *testing.T) ssh.PublicKey {
		t.Helper()
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		return kp.Public()
	}

	tests := []struct {
		name        string
		withCert    bool
		key         func(t *testing.T, f *FileAgent, identity ssh.PublicKey) ssh.PublicKey
		wantRemoved bool
	}{
		{
			name:        "should remove the key files when the key is the held public key",
			key:         func(_ *testing.T, _ *FileAgent, identity ssh.PublicKey) ssh.PublicKey { return identity },
			wantRemoved: true,
		},
		{
			name:        "should remove the key files when the key is the held certificate",
			withCert:    true,
			key:         func(_ *testing.T, _ *FileAgent, identity ssh.PublicKey) ssh.PublicKey { return identity },
			wantRemoved: true,
		},
		{
			name:        "should leave the key files in place when the key is another identity",
			key:         func(t *testing.T, _ *FileAgent, _ ssh.PublicKey) ssh.PublicKey { return other(t) },
			wantRemoved: false,
		},
		{
			name:        "should leave the key files in place when the key is another identity and a certificate is held",
			withCert:    true,
			key:         func(t *testing.T, _ *FileAgent, _ ssh.PublicKey) ssh.PublicKey { return other(t) },
			wantRemoved: false,
		},
		{
			name:        "should leave the key files in place when the key is nil",
			key:         func(_ *testing.T, _ *FileAgent, _ ssh.PublicKey) ssh.PublicKey { return nil },
			wantRemoved: false,
		},
		{
			// A certificate for the held key is not the held identity when
			// the agent has no certificate loaded, and vice versa: the
			// caller is naming material this agent is not managing.
			name:     "should leave the key files in place when the key is a certificate for the held public key",
			withCert: false,
			key: func(t *testing.T, f *FileAgent, _ ssh.PublicKey) ssh.PublicKey {
				t.Helper()
				return newTestCert(t, f.keypair.Public(), caSigner)
			},
			wantRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, identity := held(t, tt.withCert)

			if err := f.Remove(tt.key(t, f, identity)); err != nil {
				t.Fatalf("Remove() error = %v", err)
			}

			_, err := os.Stat(f.privKey)
			if tt.wantRemoved && !os.IsNotExist(err) {
				t.Errorf("expected %s to be removed, stat error = %v", f.privKey, err)
			}
			if !tt.wantRemoved && err != nil {
				t.Errorf("expected %s to be left in place, stat error = %v", f.privKey, err)
			}
		})
	}
}

// should leave the key files in place when no identity is loaded, since
// there is nothing for the key to match. RemoveAll is the way to clear
// files whose private key could not be parsed.
func TestFileAgent_Remove_NoIdentityLoaded(t *testing.T) {
	t.Parallel()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ssoossh")
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	f := &FileAgent{privKey: path}

	if err := f.Remove(kp.Public()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to be left in place, stat error = %v", path, err)
	}
}

// should report nothing removed when there is no private key file on disk.
func TestFileAgent_RemoveAll_NoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f := &FileAgent{privKey: filepath.Join(dir, "id_ssoossh")}

	n, err := f.RemoveAll()
	if err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if n != 0 {
		t.Errorf("RemoveAll() = %d, want 0", n)
	}
}

// should remove an untrusted or expired certificate identity, and leave a valid one in place.
func TestFileAgent_CleanupAgent(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	untrustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	untrustedSigner, err := ssh.NewSignerFromKey(untrustedCA.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}

	t.Run("should be a no-op when no keypair is loaded", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{}
		if err := f.CleanupAgent(); err != nil {
			t.Errorf("CleanupAgent() error = %v, want nil", err)
		}
	})

	t.Run("should be a no-op when the loaded keypair has no certificate", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		f := &FileAgent{keypair: kp, privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if err := os.WriteFile(path, []byte("dummy"), 0600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		if err := f.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to still exist, stat error = %v", path, err)
		}
	})

	t.Run("should remove key files for a certificate signed by an untrusted CA", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		leaf, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		leaf.SetCertificate(newTestCert(t, leaf.Public(), untrustedSigner))
		f := &FileAgent{keypair: leaf, privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if err := os.WriteFile(path, []byte("dummy"), 0600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		if err := f.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat error = %v", path, err)
		}
	})

	t.Run("should leave key files for a certificate signed by a trusted CA", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		leaf, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		leaf.SetCertificate(newTestCert(t, leaf.Public(), caSigner))
		f := &FileAgent{keypair: leaf, privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if err := os.WriteFile(path, []byte("dummy"), 0600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		if err := f.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to still exist, stat error = %v", path, err)
		}
	})

	t.Run("should refuse to run when no CAs are registered", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		leaf, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		leaf.SetCertificate(newTestCert(t, leaf.Public(), caSigner))
		f := &FileAgent{keypair: leaf, privKey: path}
		if err := os.WriteFile(path, []byte("dummy"), 0600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		if err := f.CleanupAgent(); err == nil {
			t.Error("CleanupAgent() error = nil, want error with no CAs registered")
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to still exist with no CAs registered, stat error = %v", path, err)
		}
	})
}

// should build one signer from the loaded keypair, or error when none is loaded.
func TestFileAgent_Signers(t *testing.T) {
	t.Parallel()

	t.Run("should error when no keypair is loaded", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{}
		if _, err := f.Signers(); err == nil {
			t.Error("Signers() error = nil, want error")
		}
	})

	t.Run("should return one signer for the loaded keypair", func(t *testing.T) {
		t.Parallel()
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		f := &FileAgent{keypair: kp}
		signers, err := f.Signers()
		if err != nil {
			t.Fatalf("Signers() error = %v", err)
		}
		if len(signers) != 1 {
			t.Errorf("Signers() returned %d signers, want 1", len(signers))
		}
	})
}

// should be a no-op, since FileAgent holds no persistent connection.
func TestFileAgent_Close(t *testing.T) {
	t.Parallel()

	f := &FileAgent{}
	if err := f.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// should always report no underlying live-agent connection.
func TestFileAgent_Agent(t *testing.T) {
	t.Parallel()

	f := &FileAgent{}
	if got := f.Agent(); got != nil {
		t.Errorf("Agent() = %v, want nil", got)
	}
}

// should require at least one CA and reject an unparseable CA string, otherwise accumulating registered CAs.
func TestFileAgent_SetCA(t *testing.T) {
	t.Parallel()

	t.Run("should reject a call with no CAs", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{}
		if err := f.SetCA(); err == nil {
			t.Error("SetCA() error = nil, want error")
		}
	})

	t.Run("should reject an unparseable CA string", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{}
		if err := f.SetCA("not a key"); err == nil {
			t.Error("SetCA() error = nil, want error")
		}
	})

	t.Run("should accumulate CAs across calls", func(t *testing.T) {
		t.Parallel()
		ca1, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		ca2, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("NewEd25519KeyPair() error = %v", err)
		}
		ca1Str, err := ca1.MarshalAuthorizedKey()
		if err != nil {
			t.Fatalf("MarshalAuthorizedKey() error = %v", err)
		}
		ca2Str, err := ca2.MarshalAuthorizedKey()
		if err != nil {
			t.Fatalf("MarshalAuthorizedKey() error = %v", err)
		}
		f := &FileAgent{}
		if err := f.SetCA(ca1Str); err != nil {
			t.Fatalf("SetCA(ca1) error = %v", err)
		}
		if err := f.SetCA(ca2Str); err != nil {
			t.Fatalf("SetCA(ca2) error = %v", err)
		}
		if len(f.cas) != 2 {
			t.Errorf("expected 2 registered CAs, got %d", len(f.cas))
		}
	})
}

// should read and validate the on-disk certificate file against registered CAs.
func TestFileAgent_Certificates(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	untrustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	untrustedSigner, err := ssh.NewSignerFromKey(untrustedCA.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should error when no CA is registered", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{privKey: filepath.Join(t.TempDir(), "id_ssoossh")}
		if _, err := f.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should error when the certificate file does not exist", func(t *testing.T) {
		t.Parallel()
		f := &FileAgent{privKey: filepath.Join(t.TempDir(), "id_ssoossh"), cas: []ssh.PublicKey{ca.Public()}}
		if _, err := f.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should error when the certificate file is unparseable", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		if err := os.WriteFile(path+"-cert.pub", []byte("not a certificate"), 0644); err != nil { //nolint:gosec // test fixture, not secret material
			t.Fatalf("WriteFile() error = %v", err)
		}
		f := &FileAgent{privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if _, err := f.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should error when the file holds a plain public key, not a certificate", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		authKey, err := leaf.MarshalAuthorizedKey()
		if err != nil {
			t.Fatalf("MarshalAuthorizedKey() error = %v", err)
		}
		if err := os.WriteFile(path+"-cert.pub", []byte(authKey), 0644); err != nil { //nolint:gosec // test fixture, not secret material
			t.Fatalf("WriteFile() error = %v", err)
		}
		f := &FileAgent{privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if _, err := f.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should error when the certificate is not signed by a trusted CA", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		cert := newTestCert(t, leaf.Public(), untrustedSigner)
		if err := os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0644); err != nil { //nolint:gosec // test fixture, not secret material
			t.Fatalf("WriteFile() error = %v", err)
		}
		f := &FileAgent{privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		if _, err := f.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should return the certificate when signed by a trusted CA", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "id_ssoossh")
		cert := newTestCert(t, leaf.Public(), caSigner)
		if err := os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0644); err != nil { //nolint:gosec // test fixture, not secret material
			t.Fatalf("WriteFile() error = %v", err)
		}
		f := &FileAgent{privKey: path, cas: []ssh.PublicKey{ca.Public()}}
		got, err := f.Certificates()
		if err != nil {
			t.Fatalf("Certificates() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Certificates() returned %d certs, want 1", len(got))
		}
	})
}

// should surface the underlying write error when the private key cannot be written to disk.
func TestFileAgent_AddKeypair_WriteError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// The parent "directory" is a regular file, so AddKeypair's MkdirAll
	// (and any write beneath it) must fail.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	f := &FileAgent{privKey: filepath.Join(blocker, "id_ssoossh")}

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	if err := f.AddKeypair(kp); err == nil {
		t.Error("AddKeypair() error = nil, want error")
	}
}

// should compare two public keys by their marshaled bytes, treating nil as never equal.
func TestPublicKeysEqual(t *testing.T) {
	t.Parallel()

	a, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	b, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	tests := []struct {
		name string
		a, b ssh.PublicKey
		want bool
	}{
		{name: "should treat a nil left side as unequal", a: nil, b: a.Public(), want: false},
		{name: "should treat a nil right side as unequal", a: a.Public(), b: nil, want: false},
		{name: "should treat both nil as unequal", a: nil, b: nil, want: false},
		{name: "should treat the same key as equal", a: a.Public(), b: a.Public(), want: true},
		{name: "should treat different keys as unequal", a: a.Public(), b: b.Public(), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := publicKeysEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("publicKeysEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}
