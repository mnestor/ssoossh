# Configuration

Every surface you configure to run ssoossh, in one place: the server, the
client, `ssh_config`, `sshd` on target hosts, and PAM. Each section shows
the settings that matter and links to the annotated reference with every
remaining default. Those references are the defaults files themselves —
each is embedded in its binary and installed under `/etc/ssoossh`, so
there is no separate sample to drift from them:

- [server/config/defaults.yaml](../server/config/defaults.yaml): full server reference
- [client/config/defaults.yaml](../client/config/defaults.yaml): full client reference
- [pam.d-sudo.example](pam.d-sudo.example): annotated PAM stack

For the operational procedures around these settings (generating the CA
key, systemd, provider setup, the PAM lockout warning), see
[deployment.md](deployment.md).

## Files at a glance

| File | Lives on | Search paths |
| --- | --- | --- |
| `ssoosshd.yaml` | the server | `./ssoosshd.yaml`, `/etc/ssoosshd.yaml`, `/etc/ssoossh/ssoosshd.yaml` |
| `ssoossh.yaml` | each client machine | `/etc/ssoossh/ssoossh.yaml`, `~/.config/ssoossh.yaml`, `./ssoossh.yaml` |
| `ssh_config` | each client machine | standard OpenSSH locations |
| `sshd_config` | each target host | standard OpenSSH locations |
| `/etc/pam.d/sudo`, `/etc/pam.d/su` | hosts using `pam_ssoossh` | fixed |

## Server: `ssoosshd.yaml`

The minimum working configuration is a CA key, a public URL, and an OIDC
client. Everything else has a working default.

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

Anyone who can read this file can sign certificates as the CA. Treat it
like the private key it contains.

### TLS

Plain HTTP only works for loopback development. Pick one:

**A) ssoosshd terminates TLS itself:**

```yaml
http:
  tls:
    certificate_file: /etc/ssoossh/tls/server.crt
    private_key_file: /etc/ssoossh/tls/server.key
```

**B) a reverse proxy terminates TLS** and forwards plain HTTP. Remove
`tls` and set:

```yaml
http:
  is_https: true
  # CIDRs of the proxy, trusted to set X-Forwarded-For/-Proto
  trusted_proxies: ["127.0.0.1/32"]
```

Two settings here fail silently until someone tries to log in:

- `http.public_url` must be what browsers reach, or the OIDC redirect URI
  is wrong and the identity provider rejects it.
- `http.trusted_proxies` must name the proxy, or `X-Forwarded-For` is
  ignored and every request (including the approver IP recorded in key
  IDs) is attributed to the proxy itself.

See [deployment.md](deployment.md#5-reverse-proxy-and-tls) for a minimal
nginx example.

### OIDC

```yaml
authentication:
  provider_url: "https://idp.example.com"   # base URL; discovery must resolve
  client_id: "..."
  client_secret: "..."
  # fields.username defaults to preferred_username
  # fields.groups is needed when cert_options.*.require_group is in use
```

The one redirect URI the provider needs is
`<http.public_url>/auth/callback`. Provider walkthrough (pocket-id):
[deployment.md](deployment.md#4-oidc-provider-setup-pocket-id).

### Database

SQLite by default; PostgreSQL for multi-instance. Migrations are explicit
and guarded against version skew.

```yaml
db:
  # SQLite (default). Under systemd, put it in the unit's StateDirectory:
  connection_string: /var/lib/ssoossh/ssoossh.db

  # PostgreSQL:
  # provider: postgres
  # connection_string: "postgres://user:pass@db.example.com/ssoossh"
```

### Certificate options and lifetime policy

`cert_options.*` is the outer bound on what any certificate may carry:
principals, extensions, `valid_duration`, `require_group` gating, and the
key ID template per certificate type. Nothing reachable over HTTP can
exceed it. Lifetimes are then narrowed per issuance by group tiers and
source-network rules; see
[certificate-lifetime-policy.md](certificate-lifetime-policy.md) for the
semantics and [server/config/defaults.yaml](../server/config/defaults.yaml)
for the syntax.

### Multi-instance and startup modes

Single-process is the default (`ssoosshd serve`, in-process pubsub).
Running more than one instance requires PostgreSQL, NATS with mTLS, and a
shared cookie key:

```yaml
multi_instance: true

http:
  cookie_key: "your-secret-key-here-32-bytes-minimum"   # same on every instance

pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/path/to/client-cert.pem"
    key_file: "/path/to/client-key.pem"
    ca_file: "/path/to/ca-cert.pem"

db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"
```

| Command | Runs | Requires |
| --- | --- | --- |
| `ssoosshd serve` | HTTP + listener + in-process signer | nothing extra |
| `ssoosshd serve api` | HTTP + listener; publishes signing jobs | `pubsub.backend: nats` |
| `ssoosshd sign` | signer only; holds the CA key, no database or HTTP | `pubsub.backend: nats`, `ssh_key` |

Both split modes fail at startup if the in-process backend is configured.
NATS setup, subject permissions, and failover behavior:
[deployment.md](deployment.md#7-running-more-than-one-instance).

### Email notifications

Off by default. Turn it on by naming a relay and a sender:

```yaml
mail:
  enabled: true
  from: "ssoossh <no-reply@example.com>"
  smtp:
    host: "localhost"
    port: 25
```

That is the local-relay case, where no TLS and no authentication are
reasonable. For a relay reached over a network both are strongly suggested,
and the server warns at startup when they are missing. Users choose which
notifications they receive at `/preferences` in the web UI.

Full reference, including what each notification contains and how to
override a template: [email-notifications.md](email-notifications.md).

## Client: `ssoossh.yaml`

One setting has no default:

```yaml
server: "https://ssh.example.com"
```

Verify it resolves before a real login. `--debug` is where resolved
settings are reported, and it works on any command:

```sh
ssoossh --debug ca
```

Everything else (key storage, key algorithm, TLS verification, whether to
use the agent) defaults sensibly; see
[client/config/defaults.yaml](../client/config/defaults.yaml). Two worth
knowing:

- `use_agent: true` (default) uses ssh-agent for key storage. If the agent
  is not reachable and `fallback_file_agent: true` is set (the default),
  the client automatically falls back to storing keys in files instead. If
  fallback is disabled (`fallback_file_agent: false`), an unreachable agent
  causes the login to fail.
- `use_agent: false` stores key files on disk instead of using an ssh-agent.
  Only the `Match exec` invocation mode works then (see below).
- `sshkey.type` / `sshkey.size` select the generated key algorithm.

### Precedence and fleet enforcement

Lowest to highest; each source is overridden by any later one that sets
the same key:

1. Built-in defaults
2. User config (`~/.config/ssoossh.yaml`, `%AppData%\ssoossh\ssoossh.yaml`)
3. Local config (`./ssoossh.yaml`, or `--config`)
4. CLI flags (`--server`, `--key-type`, `--key-size`)
5. The `enforce` file named by the machine-wide config
6. Platform policy: Windows Group Policy registry keys, macOS managed preferences

Enforcement is a guardrail, not a security boundary: the client runs as
the user. The one setting enforced beyond client cooperation is
`cert_options.*.valid_duration` on the server. Details, including the CLI
flag caveat: [client-settings-enforcement.md](client-settings-enforcement.md).

## `ssh_config`

The client is never run on its own; `ssh` invokes it. Two modes:

| | `Match exec` + `ssh login` | `ProxyCommand` |
| --- | --- | --- |
| Client after issuance | exits | stays running, relays the connection |
| ssh-agent | optional; key files on disk also work | **required** |

`ssh` reads `IdentityFile`/`CertificateFile` once at startup, which is why
`ProxyCommand` needs an agent: a certificate refreshed on disk after that
point goes unused.

**`Match exec` (recommended).** Runs before `ssh` connects. A valid,
already-loaded certificate is reused, so this adds no browser round trip
until it expires; a non-zero exit blocks the connection.

```ssh-config
Match host bastion.example.com exec "ssoossh ssh login"
    User youruser
```

**`ProxyCommand`.** Ensures a valid certificate, then hands off to
whatever relay command you give it. Useful when reaching the target also
requires an HTTP/SOCKS proxy:

```ssh-config
Host jump.example.com
    ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p
```

`ssoossh ssh config` prints both recipes, and nothing else. It is offline —
it contacts no server and needs none configured — because these lines are
what you read when the connection is the broken thing.

Service accounts are wired up from an enrollment code instead of a browser
login; `ssoossh service enroll` prints that recipe, with real paths, at the
end of a successful enrollment.

## Diagnostics: `-v` and `--debug`

Two flags, both writing to stderr so they never contaminate the certificate
on stdout, and both accepting an environment variable for the invocations
whose command line is not yours to edit — an `ssh_config` `Match exec`
line, a cron entry, a systemd unit.

| | Flag | Environment | Answers |
| --- | --- | --- | --- |
| Trace | `-v`, `-vv`, `-vvv` | `SSOOSSH_VERBOSE=1..3` | what the client did, in order |
| Report | `--debug` | `SSOOSSH_DEBUG=1` | what it resolved, and from where |

**`-v` is what to attach to a bug report.** The ladder mirrors `ssh`'s own,
which matters for a tool you invoke from `ssh_config`: `-v` traces the
high-level steps, `-vv` adds requests and file operations, `-vvv` adds
bodies. More `v`s clamp rather than error.

```sh
ssoossh -vv ssh login 2> ssoossh.log
```

**`--debug` prints the configuration report**: every config source in merge
order with what came of each (merged, absent, skipped and why), the settings
that resulted, and where key and certificate files resolve to and whether
they exist. It is deliberately printed even when startup failed, which is
when it is most wanted. `--debug` implies `-v`, so one flag gives both the
report and the steps that followed it.

```sh
SSOOSSH_DEBUG=1 ssh bastion.example.com
```

The flag is hidden from `--help` because it is a diagnostic aid rather than
part of the command's advertised surface. It is supported, not secret.

**`--debug` is the only place resolved settings are reported.** There is no
`show me the config` command printing a shorter version: two commands
answering "what is in effect" with different amounts of truth is a
maintenance trap, and the shorter one is always the one that goes stale.
`ssoossh ssh config` prints the `ssh_config` recipes and nothing more.

Run `--debug` on the command you are actually diagnosing, since some of what
it reports is decided by that command. `ssoossh ssh config --debug` needs no
server but leaves key storage and the CA unresolved, because an offline
command never sets either up; `ssoossh --debug ca` is the smallest
invocation that resolves everything.

Values in the environment variables that are not a number (`SSOOSSH_VERBOSE`)
or a boolean (`SSOOSSH_DEBUG`) read as off. A diagnostic aid must never be
the reason a login fails.

## `sshd` on target hosts

Point each target host's `sshd_config` at the CA public key so it trusts
certificates ssoosshd signs:

```sshd-config
TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub
```

Fetch the key from the running server (this confirms the deployed key,
rather than trusting a local file blindly):

```sh
ssoossh --server https://ssh.example.com ca > /etc/ssh/ssoossh_ca.pub
```

`sshd` additionally checks the validity window, enforces critical options
such as `source-address` and `force-command`, and maps allowed login names
via `AuthorizedPrincipalsFile` or `AuthorizedPrincipalsCommand`.

## PAM: `pam_ssoossh`

One line in the `auth` group of `/etc/pam.d/sudo` (and `/etc/pam.d/su` if
wanted), above the existing `pam_unix.so` line:

```
auth  sufficient  pam_ssoossh.so  server=https://ssoosshd.example.com  trusted-ca-file=/etc/ssoossh/ca.pub
```

`trusted-ca-file` is the same authorized_keys-format CA public key used
for `TrustedUserCAKeys`. Module arguments (`server`, `trusted-ca-file`,
`debug`, `insecure-skip-verify`, `skew-tolerance`, `timeout`,
`principals-map`) are documented in
[pam.d-sudo.example](pam.d-sudo.example).

**Before editing a real PAM stack, read the lockout warning and the
`sufficient` vs `required` trade-off in
[deployment.md](deployment.md#8-pam-sudo-and-su).** Getting
`/etc/pam.d/sudo` wrong costs you `sudo` on that machine.
