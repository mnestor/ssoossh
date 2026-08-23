# ssoossh

**SSO for SSH.** Users authenticate through your existing identity provider
via OIDC and receive a short-lived SSH certificate for the session, instead
of provisioning, distributing, and rotating long-lived SSH keys.

Pronounced *sue-sssh*. Self-hosted and homelab-friendly. The reference
configuration uses [pocket-id](https://github.com/pocket-id/pocket-id) as
the OIDC provider.

> **Status: early development.** User certificates and `sudo`/`su` through
> PAM work end to end today. Host and service certificates are designed and
> have server-side support; their client commands are not wired up yet.
> Interfaces and configuration are expected to change.

## The problem

SSH keys are bearer credentials with no expiry and no central revocation:
once a key lands in `authorized_keys`, nothing ties "this key is
authorized" to "this person still works here." SSH certificates solve the
mechanics (principals, constraints, expiry); what's usually missing is the
piece that decides *who* gets one, with which principals, for how long,
tied to the identity provider you already run.

ssoossh is that piece.

## How it works

`ssh` invokes the ssoossh client from `ssh_config`. The client asks the
server for a certificate, you approve the request in a browser while
logged in at your identity provider, and the client loads the signed
certificate into your ssh-agent so `ssh` can connect as normal.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh + ssoossh client
    participant Server as ssoosshd
    participant Host as target host

    User->>SSH: ssh host
    SSH->>Server: public key
    Server-->>SSH: authorization URL
    User->>Server: open URL, log in via OIDC, approve
    Server-->>SSH: signed certificate
    SSH->>Host: connect with certificate
    Host-->>User: session
```

The target host trusts the certificate because its `sshd_config` names the
ssoossh CA in `TrustedUserCAKeys`. A valid certificate is reused until it
expires, so one browser approval can cover a workday. The same approval
flow backs `sudo`/`su` through the `pam_ssoossh` PAM module, where a
certificate is requested, validated once, and discarded.

[docs/flows.md](docs/flows.md) has the full set of diagrams, step by step.

## Components

| Component | What it does |
| --- | --- |
| **ssoossh** (client) | Runs on macOS, Linux, and Windows. Invoked from `ssh_config`; manages its own keypair and talks to the server over HTTPS. Private keys never leave the machine. |
| **ssoosshd** (server) | The trust anchor and policy decision point. Authenticates via OIDC, maps identity to certificate contents, signs with the CA key, and serves the web UI where issuance is approved. |
| **pam_ssoossh** | A Linux PAM module for `sudo`/`su`. Requests a very short-lived certificate, validates it, and discards it. SSH login itself is not in scope here, since that path is already certificate-based. |

## Getting started

Four pieces make a working login:

1. Run `ssoosshd` with a CA key, a public URL, and an OIDC client:
   [docs/configuration.md](docs/configuration.md#server-ssoosshdyaml)
2. Point the client at the server in `ssoossh.yaml`:
   [docs/configuration.md](docs/configuration.md#client-ssoosshyaml)
3. Add a `Match exec "ssoossh ssh login"` line to your `ssh_config`:
   [docs/configuration.md](docs/configuration.md#ssh_config)
4. Trust the CA on each target host with `TrustedUserCAKeys`:
   [docs/configuration.md](docs/configuration.md#sshd-on-target-hosts)

[docs/getting-started.md](docs/getting-started.md) walks through them in
order; [docs/deployment.md](docs/deployment.md) is the operator runbook
behind each step.

## Documentation

| Document | What it covers |
| --- | --- |
| [docs/getting-started.md](docs/getting-started.md) | The shortest path to a working `ssh login` |
| [docs/features.md](docs/features.md) | What ssoossh solves, and everything it does today |
| [docs/flows.md](docs/flows.md) | Sequence diagrams for every flow |
| [docs/faq.md](docs/faq.md) | Common questions: users, sshd host admins, server operators |
| [docs/configuration.md](docs/configuration.md) | Every configuration surface: server, client, `ssh_config`, `sshd`, PAM |
| [docs/deployment.md](docs/deployment.md) | Operator runbook: CA key, systemd, OIDC provider, reverse proxy, multi-instance, PAM |
| [docs/decisions.md](docs/decisions.md) | What ssoossh deliberately does not do, and why |

The full index, including internals and design documents, is at
[docs/README.md](docs/README.md).

## License

[MIT](LICENSE)

## References

- SSH certificate format: [draft-ietf-sshm-cert](https://datatracker.ietf.org/doc/draft-ietf-sshm-cert/)
- SSH Transport Layer Protocol: [RFC 4253](https://www.rfc-editor.org/info/rfc4253/), for public key algorithm names, key blob and signature wire formats
- PAM specification: [RFC 86.0](https://github.com/linux-pam/linux-pam/blob/master/doc/specs/rfc86.0.txt)
- [pocket-id](https://github.com/pocket-id/pocket-id): OIDC provider used in the reference configuration
- [lldap](https://github.com/lldap/lldap): LDAP directory used in the reference configuration
