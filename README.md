# ssoossh

**SSO for SSH.** Users authenticate through your existing identity provider
via OIDC and receive a short-lived SSH certificate for the session, instead
of provisioning, distributing, and rotating long-lived SSH keys.

Pronounced *sue-sssh*. Self-hosted and homelab-friendly. The reference
configuration uses [pocket-id](https://github.com/pocket-id/pocket-id) as
the OIDC provider.

> **Status: early development.** The first release covers user certificates
> and `sudo`/`su` through PAM. Interfaces and configuration are expected to
> change.

## The problem

SSH keys are bearer credentials with no expiry and no central revocation —
once a key lands in `authorized_keys`, nothing ties "this key is
authorized" to "this person still works here." SSH certificates solve the
mechanics (principals, constraints, expiry); what's usually missing is the
piece that decides *who* gets one, with which principals, for how long —
tied to the identity provider you already run.

ssoossh is that piece. See [docs/features.md](docs/features.md) for
everything it does.

## How it works

1. Your `ssh_config` invokes the ssoossh client via `ProxyCommand` or
   `Match exec`.
2. The client generates a fresh SSH keypair (if it doesn't already hold a
   valid certificate) and sends the public key to the ssoossh server.
3. The server hands back a URL. The client opens it in a browser and waits.
4. You authenticate at your identity provider via OIDC.
5. The server signs the public key into a certificate carrying the
   principals, options, and validity window your policy allows.
6. The client loads the keypair and certificate into your ssh-agent (or
   writes them to disk), and `ssh` proceeds as normal.

### Step 1 — `ssh` invokes the client, which requests a certificate

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh
    participant Client as ssoossh client
    participant Server as ssoossh server

    User->>SSH: ssh host
    SSH->>Client: invoke (ProxyCommand / Match exec)
    Client->>Client: generate keypair (if no valid certificate)
    Client->>Server: send public key
    Server-->>Client: authorization URL
    Client->>User: present authorization URL
    Client->>Server: open SSE stream and wait
```

### Step 2 — the user authenticates and approves in a browser

While the client waits on its SSE stream, the user completes the OIDC flow
and the server signs the certificate.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser
    participant Server as ssoossh server
    participant IdP as identity provider (OIDC)

    User->>Browser: open authorization URL
    Browser->>Server: authorization request
    Server->>IdP: OIDC authentication
    IdP-->>Server: identity claims
    Server->>User: Presents user information for approval
    User->>Server: approve
    Server->>Server: sign certificate
    Note over Server: delivered to the client<br/>waiting in step 1
```

### Step 3 — the client loads the certificate and `ssh` connects

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Client as ssoossh client
    participant SSH as ssh
    participant Host as target host sshd

    Note over Client: certificate arrives over<br/>the SSE stream from step 1
    Client->>Client: load into ssh-agent, or write to disk
    Client-->>SSH: exit, certificate ready
    SSH->>Host: connect, present certificate
    Host-->>User: session
```

Host sshd server will validate Certificate based on `TrustedUserCAKeys` setting

The same approval flow covers `sudo`/`su` through the `pam_ssoossh` PAM
module: a certificate is requested, validated once, and discarded — nothing
is retained on disk or in an agent. See
[docs/flows.md](docs/flows.md) for the full set of diagrams, including
service certificate enrollment and the PAM path.

## Components

| Component | What it does |
| --- | --- |
| **ssoossh** (client) | Runs on macOS, Linux, and Windows. Invoked from `ssh_config`; manages its own keypair and talks to the server over HTTPS. |
| **ssoosshd** (server) | The trust anchor and policy decision point. Authenticates via OIDC, maps identity to certificate contents, signs with the CA key, and serves the web UI where issuance is approved. |
| **pam_ssoossh** | A Linux PAM module for `sudo`/`su`. Requests a very short-lived certificate, validates it, and discards it — SSH login itself is not in scope here, since that path is already certificate-based. |

See [docs/features.md](docs/features.md) for the full feature set and
[docs/decisions.md](docs/decisions.md) for what ssoossh deliberately does
not do.

## Getting started

The bare minimum to get a client and server talking. Everything below has
further defaults you'll likely want to change for a real deployment —
systemd unit, `sshd` configuration, OIDC provider setup, and the PAM
runbook live under [docs/](docs/).

### `ssoosshd.yaml` (server)

Searched for at `./ssoosshd.yaml`, `/etc/ssoosshd.yaml`, or
`/etc/ssoossh/ssoosshd.yaml`. Everything not shown here has a working
default — see [docs/ssoosshd.yaml.default](docs/ssoosshd.yaml.default) for
the full reference.

```yaml
# The CA private key ssoosshd signs certificates with. Inline PEM, not a
# file path. Generate one with: ssh-keygen -t ed25519 -f ca -N ""
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  -----END OPENSSH PRIVATE KEY-----

http:
  # The scheme and host browsers actually reach this deployment at.
  # Required for the OIDC redirect URI and the CSRF origin check.
  public_url: "https://ssh.example.com"

  # Plain HTTP only works for loopback development. Pick one:

  # A) ssoosshd terminates TLS itself:
  tls:
    certificate_file: /etc/ssoossh/tls/server.crt
    private_key_file: /etc/ssoossh/tls/server.key

  # B) a reverse proxy terminates TLS and forwards plain HTTP — remove
  #    `tls` above and set these instead:
  # is_https: true
  # # CIDRs of the proxy, trusted to set X-Forwarded-For/-Proto
  # trusted_proxies: ["127.0.0.1/32"]

authentication:
  client_id: "..."
  client_secret: "..."
  provider_url: "https://idp.example.com"
```

### `ssoossh.yaml` (client)

Searched for at `/etc/ssoossh/ssoossh.yaml`, `~/.config/ssoossh.yaml`, and
`./ssoossh.yaml`. One setting has no default:

```yaml
server: "https://ssh.example.com"
```

See [client/config/defaults.yaml](client/config/defaults.yaml) for what
everything else defaults to (key storage, key algorithm, TLS verification).

### `ssh_config`

The client is never run on its own — `ssh` invokes it, either through
`Match exec` or `ProxyCommand`. They differ in when the client exits and in
whether an `ssh-agent` is required:

| | `Match exec` + `ssh login` | `ProxyCommand` |
| --- | --- | --- |
| Client after issuance | exits | stays running, relays the connection |
| ssh-agent | optional — key files on disk also work | **required** — `ssh` reads key files once at startup and won't see a refreshed certificate |

#### `Match exec` (recommended)

`ssoossh ssh login` runs before `ssh` connects. A valid, already-loaded
certificate is reused, so this adds no browser round trip until it expires;
a non-zero exit blocks the connection.

```ssh-config
Match exec "ssoossh ssh login" host "*.internal.example.com"
```

#### `ProxyCommand`

Ensures a valid certificate, then hands off to whatever relay command you
give it — useful when reaching the target also requires going through an
HTTP/SOCKS proxy:

```ssh-config
Host jump.example.com
    ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p
```

Requires `ssh-agent`: `ssh` only reads `IdentityFile`/`CertificateFile` once
at startup, so a certificate refreshed on disk after that point goes unused.

### sshd

Point the target host's `sshd_config` at the CA's public key so it trusts
certificates ssoosshd signs:

```sshd-config
TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub
```

`ssoossh ca` prints that public key from the configured server.

## License

[MIT](LICENSE)

## References

- SSH certificate format — [draft-ietf-sshm-cert](https://datatracker.ietf.org/doc/draft-ietf-sshm-cert/)
- SSH Transport Layer Protocol — [RFC 4253](https://www.rfc-editor.org/info/rfc4253/) — public key algorithm names, key blob and signature wire formats
- PAM specification — [RFC 86.0](https://github.com/linux-pam/linux-pam/blob/master/doc/specs/rfc86.0.txt)
- [pocket-id](https://github.com/pocket-id/pocket-id) — OIDC provider used in the reference configuration
- [lldap](https://github.com/lldap/lldap) — LDAP directory used in the reference configuration
