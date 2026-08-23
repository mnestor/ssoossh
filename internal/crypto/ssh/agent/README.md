# agent

`internal/crypto/ssh/agent` manages an SSH keypair and certificate without
callers needing to know whether the backing store is a live SSH agent or a
pair of files on disk. Every implementation satisfies the same `Agent`
interface, so code that adds a keypair, lists identities, or checks
certificates works the same way regardless of backend.

## Backends

| Constructor | `Type()` | `Backend()` | Notes |
|---|---|---|---|
| `NewSSHAgent()` | `AgentTypeSsh` | one of the below | Picks the best available live agent for the current OS. |
| `NewOpenSSHAgent()` | `AgentTypeSsh` | `BackendOpenSSHAgent` | Standard OpenSSH agent (`SSH_AUTH_SOCK` on Unix, the `openssh-ssh-agent` named pipe on Windows). |
| `NewPageantAgent()` | `AgentTypeSsh` | `BackendPageant` | Windows only. PuTTY's Pageant. |
| `NewWSLAgent()` | `AgentTypeSsh` | `BackendWSLAgent` | Windows only. A relay named pipe (default `\\.\pipe\wsl-ssh-agent`, override with `WSL_SSH_AGENT_PIPE`) bridging into an ssh-agent running inside WSL. |
| `NewFileAgent(path)` | `AgentTypeFile` | `BackendFile` | No agent process; reads/writes `path`, `path.pub`, and `path-cert.pub` directly. |

`NewSSHAgent()` on Windows tries Pageant, then the native OpenSSH pipe, then
the WSL relay pipe, in that order, and returns whichever connects first. On
Unix it's equivalent to `NewOpenSSHAgent()`.

Use `Type()` when you need the coarse agent-vs-file distinction (e.g.
choosing how to render `ssh_config`). Use `Backend()` only for diagnostics/
logging — it should not drive behavior, since the whole point of the `Agent`
interface is that callers don't need to care which live agent they're
talking to.

## Basic usage

```go
import "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"

// Connect to whatever live agent is available, falling back to files.
ag, err := agent.NewSSHAgent()
if err != nil {
    ag, err = agent.NewFileAgent("~/.ssh/id_ssoossh")
}
if err != nil {
    return err
}
defer ag.Close()

// Trust one or more CAs. Safe to call multiple times; each call adds to the
// existing set rather than replacing it.
if err := ag.SetCA(caPublicKeyString); err != nil {
    return err
}

// Add a freshly-issued keypair + certificate.
if err := ag.AddKeypair(kp); err != nil { // kp is a *keypair.SshKeypair
    return err
}

// List only the certificate identities signed by a trusted CA.
certIdentities, err := ag.List(true)

// List everything the agent knows about, unfiltered.
allIdentities, err := ag.List(false)

// Get the parsed certificates directly.
certs, err := ag.Certificates()

// Remove anything that's expired or not signed by a trusted CA.
if err := ag.CleanupAgent(); err != nil {
    return err
}
```

## The `Agent` interface

```go
type Agent interface {
    Type() string
    Backend() string
    List(filterByCA bool) ([]*ssh.PublicKey, error)
    Add(key interface{}) error
    Remove(key ssh.PublicKey) error
    RemoveAll() (int, error)
    Signers() ([]ssh.Signer, error)
    Close() error
    Agent() agent.Agent
    SetCA(cas ...string) error
    Certificates() ([]*ssh.Certificate, error)
    AddKeypair(keypair *keypair.SshKeypair) error
    CleanupAgent() error
}
```

- **`SetCA(cas ...string)`** — registers one or more trusted CA public keys,
  in authorized_keys format or raw base64. Calls are additive: calling
  `SetCA` twice trusts the union of both calls, it never resets the set.
  Required before `List(true)`, `Certificates()`, or `CleanupAgent()` will
  find anything.
- **`List(filterByCA bool)`** — `true` returns only `ssh.Certificate`
  identities signed by a registered CA; `false` returns every identity the
  backend knows about, certificate or not.
- **`Certificates()`** — like `List(true)`, but returns parsed
  `*ssh.Certificate` values instead of `*ssh.PublicKey`. Errors if no CA is
  registered or nothing valid is found.
- **`CleanupAgent()`** — removes identities that are expired or not signed by
  a registered CA. For `FileAgent` this deletes the key files entirely,
  since a `FileAgent` manages exactly one identity. Errors if no CA is
  registered rather than treating every certificate as invalid and removing
  material that may be perfectly good.
- **`AddKeypair(kp *keypair.SshKeypair)`** — adds a keypair (see the sibling
  `keypair` package) and its certificate to the backend: `Add` on a live
  agent, or writing `path`/`path.pub`/`path-cert.pub` for `FileAgent`.
  `FileAgent` creates the parent directory when missing, verifies each file
  actually exists on disk after writing (a key that silently lands nowhere
  is this agent's worst failure mode), and removes a stale `path-cert.pub`
  when the new keypair carries no certificate.
- **`Add`/`Remove`/`Signers`/`Agent()`** — lower-level, closer to the
  underlying `golang.org/x/crypto/ssh/agent.Agent` API. `FileAgent` doesn't
  support arbitrary `Add`/`Remove` (it always manages its single configured
  identity) and returns `nil` from `Agent()` since it has no live connection.

## Choosing a backend generically

Callers that don't care which backend they get (the common case — see
`cmd/ssoossh`) should hold a value of type `Agent` and try constructors in
priority order:

```go
var ag agent.Agent
ag, err := agent.NewSSHAgent()
if err != nil && fallbackToFile {
    ag, err = agent.NewFileAgent(identityFilePath)
}
if err != nil {
    return fmt.Errorf("no agent available: %w", err)
}
```

From this point on, nothing in the calling code needs an `if`/`switch` on
which backend was chosen — every `Agent` method behaves consistently. The
only place backend identity should matter is presentation, e.g. printing
different `ssh_config` snippets for `AgentTypeSsh` vs `AgentTypeFile`.

## Certificate validation

`CertificateValid(cert *ssh.Certificate, cas []ssh.PublicKey) bool` is the
shared validity check used by both backends: the certificate must have
non-zero `ValidAfter`/`ValidBefore`, must not currently be expired, and must
be signed by one of the given CAs. It's exported for callers that need to
validate a certificate outside of an `Agent` (e.g. one read from a file
directly).
