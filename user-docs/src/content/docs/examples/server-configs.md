---
title: Server configuration examples
description: Complete, working ssoosshd.yaml configurations for common deployments.
eyebrow: Examples
sidebar:
  order: 1
---

Complete configurations rather than isolated keys. Each one is a file you could
put at `/etc/ssoossh/ssoosshd.yaml` and start from. Every key used here is
documented in the [configuration reference](/ssoossh/reference/config/).

## The minimum

A CA key, a public URL, and an OIDC client. Everything else has a working
default.

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

authentication:
  client_id: "..."
  client_secret: "..."
  provider_url: "https://idp.example.com"
```

:::danger
Anyone who can read this file can sign certificates as the CA. Treat it like the
private key it contains.
:::

The one redirect URI your identity provider needs is
`<http.public_url>/auth/callback`.

## ssoosshd terminates TLS itself

Plain HTTP only works for loopback development.

```yaml
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  -----END OPENSSH PRIVATE KEY-----

http:
  public_url: "https://ssh.example.com"
  address: "0.0.0.0"
  port: 443
  tls:
    certificate_file: /etc/ssoossh/tls/server.crt
    private_key_file: /etc/ssoossh/tls/server.key

authentication:
  client_id: "..."
  client_secret: "..."
  provider_url: "https://idp.example.com"
```

Paths only. PEM pasted inline is not accepted, because it could not then be
rotated without rewriting this file.

## Behind a reverse proxy

When something else terminates TLS, ssoosshd needs to know which hop to believe
about the client's address. `trusted_proxies` is empty by default, which means
no proxy is trusted and `X-Forwarded-For` is ignored entirely.

```yaml
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  -----END OPENSSH PRIVATE KEY-----

http:
  # What browsers reach, not what ssoosshd binds. The proxy terminates TLS,
  # so this stays https even though the listener below is plain.
  public_url: "https://ssh.example.com"
  address: "127.0.0.1"
  port: 8080

  # The proxy's own address, in CIDR form. Only these hops are believed
  # about X-Forwarded-For / X-Forwarded-Proto.
  trusted_proxies:
    - "127.0.0.1/32"

  # The session cookie is marked Secure automatically because public_url is
  # https; cookie_secure only exists to override that inference.

authentication:
  client_id: "..."
  client_secret: "..."
  provider_url: "https://idp.example.com"
```

:::caution
`trusted_proxies` is an HTTP-level setting, for a proxy that sets
`X-Forwarded-For`. A TCP-level proxy that prefixes connections with a PROXY
protocol header is a different mechanism and a different key:
[`http.proxy_protocol`](/ssoossh/reference/config/http/#proxy_protocol).
:::

## PostgreSQL

SQLite is the default and is a file path. PostgreSQL takes a standard URL.

```yaml
db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"
  max_open_conns: 25
  max_idle_conns: 10
```

For SQLite under systemd, put the file in the unit's `StateDirectory`:

```yaml
db:
  connection_string: /var/lib/ssoossh/ssoossh.db
```

## Multi-instance

More than one ssoosshd process sharing a database. This requires PostgreSQL,
NATS with mTLS, and -- enforced at startup -- an explicit cookie key, so
sessions survive a request landing on a different instance.

```yaml
multi_instance: true

http:
  public_url: "https://ssh.example.com"
  # The same value on every instance. Without it, each process would sign
  # sessions with its own random key and users would be logged out at random.
  cookie_key: "your-secret-key-here-32-bytes-minimum"

db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"

pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/path/to/client-cert.pem"
    key_file: "/path/to/client-key.pem"
    ca_file: "/path/to/ca-cert.pem"
```

The startup modes that go with it:

| Command | Runs | Requires |
| --- | --- | --- |
| `ssoosshd serve` | HTTP, listener, and in-process signer | nothing extra |
| `ssoosshd serve api` | HTTP and listener; publishes signing jobs | `pubsub.backend: nats` |
| `ssoosshd sign` | the signer only; holds the CA key, no database or HTTP | `pubsub.backend: nats` and `ssh_key` |

Both split modes fail at startup if the in-process pubsub backend is configured.

## Roles and containment

All four group names are optional, and each is an OIDC group name. Empty
disables or narrows the role rather than opening it up -- authorization fails
closed.

```yaml
admin:
  # Full administrative access: re-enabling users, plus everything SOC and
  # auditor can do.
  require_group: "ssh-admins"

  # Containment only: disabling users and expiring enrollments. Admins hold
  # this regardless, so leaving it empty narrows SOC actions to admins.
  soc_group: "security-ops"

  # Read-only: effective configuration, cross-user certificate history.
  auditor_group: "ssh-auditors"

  # Shown on the account-disabled page.
  contact_email: "ssh-help@example.com"
```

Because authorization is evaluated from the session identity, the session
lifetime is the revocation window: removing someone from an admin group in the
identity provider takes effect at their next login.

## Email notifications

Off by default. Naming a relay and a sender turns it on.

```yaml
mail:
  from: "ssoossh@example.com"
  reply_to: "ssh-admins@example.com"
  smtp:
    server_name: "smtp.example.com"
    username: "ssoossh@example.com"
    # Read from a file so the secret is not in this config.
    password_file: "/run/secrets/ssoossh-smtp-password"
    helo: "ssoossh.example.com"
```

Every mail key is on the [`mail` reference pages](/ssoossh/reference/config/mail/).

## Key ID templating

The key ID is what shows up in an audit log on the target host, so it is worth
making it identify the person.

```yaml
authentication:
  fields:
    # Capture extra claims at login, under names your templates can use.
    extra:
      dept: "https://idp.example.com/department"
      accounts: altAccounts

cert_options:
  user:
    key_id_template: '{{.Username}}-{{.Extra.dept}}'
  pam:
    # PAM deliberately never falls back to the user template, so a sudo and
    # an SSH login by the same person stay distinguishable.
    key_id_template: 'sudo:{{.Username}}'
```

A field with no value at issuance renders as `MISSING` rather than collapsing to
an empty string, so a key ID shows an auditable gap (`alice-MISSING`) instead of
quietly losing a segment. A typo in a *standard* field name, or malformed
template syntax, fails at startup instead -- every configured template is parsed
and test-executed once at boot.

## FIPS

```yaml
# Steers the CA key, client-submitted public keys, and the TLS profile toward
# FIPS 140-3 approved algorithms. A non-approved algorithm becomes a hard
# error rather than a warning.
fips: true
```

Leaving it unset is not the same as `false`: unset follows the Go runtime's own
FIPS 140-3 mode, which is why the key ships with no value rather than set to
false.
