# keypair

`internal/crypto/ssh/keypair` generates, loads, and marshals SSH keypairs and
their associated signed certificates, independent of the underlying
algorithm. Callers get back the same `*SSHKeypair` type — and can program
against the `Keypair` interface — regardless of whether it holds an RSA,
ECDSA, or Ed25519 key.

## Supported key types

| Type | Generate | Load from PEM | Notes |
| --- | --- | --- | --- |
| RSA | `NewRSAKeyPair(bits)` | `LoadSSHKeypair` | Minimum 2048 bits. |
| ECDSA | `NewECDSAKeyPair(bits)` | `LoadSSHKeypair` | `bits` selects the curve: 256 (P-256), 384 (P-384), or 521 (P-521). |
| Ed25519 | `NewEd25519KeyPair()` | `LoadSSHKeypair` | |

DSA (`ssh-dss`) is intentionally not supported — it's deprecated and removed
from modern OpenSSH clients. FIDO2/security-key-backed types
(`sk-ecdsa-sha2-nistp256@openssh.com`, `sk-ssh-ed25519@openssh.com`) are also
out of scope: this package's certificates are short-lived tokens (well under
a minute of expected life), and hardware-security-key types don't fit that
usage pattern.

## Basic usage

```go
import "github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"

// Generate. keyType is "rsa", "ecdsa", or "ed25519"; keySize is bits for rsa
// (>=2048), the curve size for ecdsa (256/384/521), and ignored for ed25519.
kp, err := keypair.NewSSHKeypair("ed25519", 0)
if err != nil {
    return err
}

// Attach a certificate signed by your CA (e.g. after calling out to a
// signing service) and get it back out.
kp.SetCertificate(cert) // cert is an *ssh.Certificate
certStr := kp.MarshalCertificate()

// Marshal for storage — e.g. handing off to internal/crypto/ssh/agent's
// FileAgent.AddKeypair or a live agent's Agent.AddKeypair.
privPEM, err := kp.MarshalPrivateKey()
pubStr, err := kp.MarshalAuthorizedKey()

// Reload later. LoadSSHKeypair auto-detects the PEM block type (RSA/EC/
// PKCS#8/OpenSSH) and returns the right kind of key.
kp2, err := keypair.LoadSSHKeypair(privPEM)
```

## The `Keypair` interface

```go
type Keypair interface {
    Private() interface{}
    Public() ssh.PublicKey
    MarshalAuthorizedKey() (string, error)
    MarshalPrivateKey() ([]byte, error)
    Certificate() *ssh.Certificate
    SetCertificate(cert *ssh.Certificate)
    SignedBy(ca ssh.PublicKey) bool
    CertificateString() (string, error)
    MarshalCertificate() []byte
    ParseCertificateFromString(certStr string) error
}
```

`*SSHKeypair` implements `Keypair`. Code that only needs to hold, marshal, or
check a keypair — as opposed to code that specifically needs to *generate*
one of a particular algorithm — should take a `keypair.Keypair` rather than
the concrete `*keypair.SSHKeypair`, so it isn't coupled to which algorithm
produced it.

- **`Private()`** — the raw private key: `*rsa.PrivateKey`,
  `*ecdsa.PrivateKey`, or `ed25519.PrivateKey`. Callers that need to sign
  typically wrap this with `ssh.NewSignerFromKey`.
- **`Public()`** — the SSH public key derived from the private key.
- **`MarshalAuthorizedKey()`** — the public key in authorized_keys format.
- **`MarshalPrivateKey()`** — the private key PEM-encoded: `RSA PRIVATE KEY`
  for RSA, `EC PRIVATE KEY` for ECDSA, or an OpenSSH-format
  `OPENSSH PRIVATE KEY` block for Ed25519 (which has no standard PKCS#1/
  SEC1-style PEM form). Reload the result with `LoadSSHKeypair`.
- **`SetCertificate` / `Certificate`** — attach and retrieve the signed
  `*ssh.Certificate` for this keypair.
- **`SignedBy(ca)`** — true if a certificate is set and its signature key
  matches `ca`.
- **`CertificateString()` / `MarshalCertificate()`** — the certificate in
  authorized_keys format, as a `string` or `[]byte` respectively.
  `CertificateString` errors if no certificate is set; `MarshalCertificate`
  returns `nil`.
- **`ParseCertificateFromString(certStr)`** — parse an authorized_keys-format
  certificate string (e.g. from a signing service response or a
  `*-cert.pub` file) and attach it via `SetCertificate`.

## Loading: which function to use

- **`LoadSSHKeypair(data []byte)`** — the general entry point. Give it any
  PEM-encoded private key and it detects the type from the PEM block header
  and the key algorithm inside it: `RSA PRIVATE KEY` (PKCS#1), `EC PRIVATE
  KEY` (SEC1), `PRIVATE KEY` (PKCS#8 — RSA, ECDSA, or Ed25519), or
  `OPENSSH PRIVATE KEY` (RSA, ECDSA, or Ed25519). Use this unless you already
  know the exact algorithm and PEM format and want to skip the detection.
- **`LoadSSHKeypair` is the only loader.** The per-algorithm loaders
  (`LoadRSAKeyPair`, `LoadECDSAKeyPair`, `LoadEd25519KeyPair`) were removed:
  they duplicated `LoadSSHKeypair`'s cases and had no callers.

## Relationship to `internal/crypto/ssh/agent`

This package doesn't know about agents or files — it only handles key
material and certificates in memory. `internal/crypto/ssh/agent` consumes
`*SSHKeypair` values (via `Agent.AddKeypair`) to add them to a live ssh-agent
or write them to disk; see that package's README for the full flow from
"generate a keypair" to "the identity is usable over SSH".
