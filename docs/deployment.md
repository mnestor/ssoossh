# Deployment

Operator-facing runbook for bringing up `ssoosshd`, wiring `sshd` and
`sudo`/`su` to trust it, and pointing it at an OIDC provider.

If you just want the shortest path to a working login, start with
[getting-started.md](getting-started.md) instead — this document is the
reference it links back to.

Two ways to run `ssoosshd`: the systemd unit below (installed from the
`.deb`/`.rpm` package), or the Docker Compose deployment at
[../deploy/docker-compose.yml](../deploy/docker-compose.yml). Both need the
same configuration; this doc mostly matters regardless of which one you run.

## 1. CA key

`ssoosshd` signs with an SSH CA keypair. Generate one and paste the private
key into `ssh_key` in `ssoosshd.yaml` — it's inline PEM, not a file path
(see `docs/ssoosshd.yaml.default`):

```sh
ssh-keygen -t ed25519 -f ca -C "ssoossh CA" -N ""
```

This writes `ca` (private key) and `ca.pub` (public key). Put `ca`'s
contents under `ssh_key:` in `ssoosshd.yaml` as a literal block scalar
(`ssh_key: |` followed by the indented key), then treat the plaintext file
as sensitive — delete it or move it somewhere access-controlled once it's
in the config, since anyone who can read `ssoosshd.yaml` can sign
certificates as this CA.

## 2. systemd

The `ssoosshd` package (`.deb`/`.rpm`)
installs the binary at `/usr/local/sbin/ssoosshd` and the annotated config
sample at `/etc/ssoossh/ssoosshd.yaml`. It does not currently install a
systemd unit or create a service account — do both by hand:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin ssoossh
install -o ssoossh -g ssoossh -m 0750 -d /var/lib/ssoossh
chown -R ssoossh:ssoossh /etc/ssoossh
cp deploy/ssoosshd.service /etc/systemd/system/ssoosshd.service
systemctl daemon-reload
systemctl enable --now ssoosshd
```

See [../deploy/ssoosshd.service](../deploy/ssoosshd.service) for what the
unit actually sandboxes (`ReadOnlyPaths=/etc/ssoossh`,
`StateDirectory=ssoossh` for the default sqlite database file, etc.) — the
comments there explain each choice, not repeated here.

Set `db.connection_string` in `ssoosshd.yaml` to a path under
`/var/lib/ssoossh/` (e.g. `/var/lib/ssoossh/ssoossh.db`) so the sqlite file
lands in the directory the unit already owns and persists across restarts.

## 3. sshd: TrustedUserCAKeys

Once `ssoosshd` is running, fetch its CA public key with the client (this
is the recommended way — it confirms the running server's key matches
what you loaded, rather than trusting the file from step 1 blindly):

```sh
ssoossh --server https://ssh.example.com ca > /etc/ssh/ca.pub
```

Then on every host that should accept certificates from this CA, in
`/etc/ssh/sshd_config`:

```
TrustedUserCAKeys /etc/ssh/ca.pub
```

and `systemctl reload sshd`.

## 4. Client: `ssh_config` recipes

Two invocation modes (see `client/cmd/ssh_login.go` and
`ssh_proxycommand.go`), chosen per the note on file-based keys below.

**`Match exec` + `ssh login`** (recommended). Runs before `ssh` connects,
loading a certificate into the agent (or writing key files) if none valid
is already loaded. Works whether or not an agent is present:

```
Match host bastion.example.com exec "ssoossh ssh login"
    User youruser
```

**`ProxyCommand`**. Ensures a certificate, then relays the connection
itself — requires an agent, because `ssh` reads identity files once at
startup and will not see a certificate written after that:

```
Host bastion.example.com
    ProxyCommand ssoossh ssh proxycommand /usr/bin/nc %h %p
```

If `use_agent: false` in `ssoossh.yaml` (key files instead of an agent),
only the `Match exec` form works.

## 5. OIDC provider setup (pocket-id)

[pocket-id](https://github.com/pocket-id/pocket-id) is the reference
provider — homelab-friendly, and what the project's own reference config
assumes — but any OIDC-compliant provider works the same way.

1. Run pocket-id (its own container or binary) and complete its first-run
   admin setup.
2. In pocket-id's admin UI, create an OIDC client for ssoossh. Set its
   callback URL to `<http.public_url>/auth/callback` — this is the one
   redirect URI the client needs. Note the generated client ID and secret.
3. Create (or import) the user(s) who will log in, and any groups needed
   for `cert_options.*.require_group` gating (e.g. an "SSH Sudoers" group
   for PAM — see §7 below).
4. In `ssoosshd.yaml`'s `authentication` section, set `provider_url` to
   pocket-id's base URL and `client_id`/`client_secret` to the values from
   step 2. Leave `fields.username` at the default `preferred_username` and
   set `fields.groups` if group-gated certificate types are in use.
5. Restart `ssoosshd` and confirm
   `GET <provider_url>/.well-known/openid-configuration` resolves from the
   server — this is the first thing that fails on a typo'd `provider_url`.

## 6. Reverse proxy and TLS

Terminating TLS in front of `ssoosshd` (nginx, Caddy, Traefik, etc.) rather
than in the process itself is the common case. Two settings matter and are
easy to get wrong because the failure is silent until someone tries to log
in:

- **`http.public_url`** — the scheme and host browsers actually reach this
  deployment at, e.g. `https://ssh.example.com`. Both the OIDC redirect URI
  and the CSRF origin check are derived from it; a proxy in front without
  this set produces a redirect URI the identity provider rejects.
- **`http.is_https: true`** if you don't set `public_url`'s scheme to
  `https` some other way — it only matters when `public_url` is unset,
  since `public_url`'s scheme wins.

Minimal nginx example, proxying to `ssoosshd` listening on
`127.0.0.1:8080`:

```nginx
server {
    listen 443 ssl;
    server_name ssh.example.com;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Also set `http.trusted_proxies` to the proxy's address (e.g.
`["127.0.0.1/32"]`) — otherwise `X-Forwarded-For` is ignored and every
request is attributed to the proxy's own IP, including in the approver
client-IP key ID field.

## 6.5. Startup modes: full, api, and sign

`ssoosshd` can run in three modes, selected by subcommand:

### Full mode (default): `ssoosshd serve` or `ssoosshd serve full`

Runs all components in one process:
- HTTP server
- Certificate approval listener
- In-process signer (holds CA key)

**Use case:** Single-instance deployments, development, testing.

**Configuration:** Standard `ssoosshd.yaml`; pubsub backend defaults to `gochannel`
(in-process). No additional setup needed.

**Invocation:**
```bash
ssoosshd serve        # Implicit full mode
ssoosshd serve full   # Explicit full mode
```

### API-only mode: `ssoosshd serve api`

Runs the HTTP server and listener without the signer:
- HTTP server
- Certificate approval listener
- Publishes signing jobs to NATS
- No CA key, no database access for signing

**Use case:** Multi-instance deployments where the signer is isolated.

**Configuration:** Must set `pubsub.backend: nats` and provide NATS URL + mTLS
credentials. Fails at startup if gochannel is configured (signing jobs would go
nowhere).

**Invocation:**
```bash
ssoosshd serve api
```

**Systemd unit:**
```ini
[Service]
ExecStart=/usr/local/sbin/ssoosshd serve api
```

### Signer-only mode: `ssoosshd sign`

Runs only the certificate signing component:
- NATS connection
- CA key access
- Consumes signing jobs from NATS
- Publishes signed certificates

**Use case:** CA key isolation — run the signer on a separate machine with
restricted network access.

**Configuration:** Requires NATS configuration (`pubsub.backend: nats`) and CA
key (`ssh_key`). Does not need or use database, HTTP, OIDC, or LDAP settings.
Fails at startup if gochannel is configured (cannot bridge separate processes).

**Invocation:**
```bash
ssoosshd sign
```

**Systemd unit:**
```ini
[Service]
ExecStart=/usr/local/sbin/ssoosshd sign
```

## 7. Running more than one instance

Multi-instance deployment means running multiple `ssoosshd` processes against
a shared database. This enables horizontal scaling and load balancing.

**Deployment topology:** Several `ssoosshd serve api` instances (HTTP +
listener) behind a load balancer, each communicating with NATS. One or more
`ssoosshd sign` processes (signer only) also connected to NATS. All instances
share a PostgreSQL database.

**Requires:**
- A shared PostgreSQL database (SQLite is single-connection only)
- NATS as the message broker (see §6.5 for mode options)
- mTLS client certificates for NATS authentication
- An explicit session cookie key (not per-process random)
- Multi-instance mode enabled (`multi_instance: true`)

If you only run one process, skip this section and use the default full mode
with `pubsub.backend: gochannel` (in-process).

### Setting up NATS

NATS is the message broker that carries certificate approvals, signatures,
and delivery notifications across instances. Each instance must be able to
reach the NATS server over the network.

#### Option 1: NATS in Docker (development)

```bash
docker run -p 4222:4222 nats:latest
```

Listens on port 4222, no authentication. Not suitable for production.

#### Option 2: NATS with mTLS (production)

NATS requires mutual TLS authentication. Generate or obtain:
- NATS server certificate and key
- CA certificate to verify the server
- Client certificates and keys for each `ssoosshd` instance

If using a local CA:

```bash
# CA
openssl genrsa -out ca-key.pem 2048
openssl req -new -x509 -days 365 -key ca-key.pem -out ca-cert.pem

# Server (sign the server certificate with your CA)
openssl genrsa -out server-key.pem 2048
openssl req -new -key server-key.pem -out server.csr
openssl x509 -req -in server.csr -CA ca-cert.pem -CAkey ca-key.pem \
  -CAcreateserial -out server-cert.pem -days 365

# Client (sign for each ssoosshd instance)
openssl genrsa -out client-key.pem 2048
openssl req -new -key client-key.pem -out client.csr
openssl x509 -req -in client.csr -CA ca-cert.pem -CAkey ca-key.pem \
  -CAcreateserial -out client-cert.pem -days 365
```

Configure NATS to require client certificates (see NATS documentation for
`tls` and `authorization` blocks).

#### NATS authorization (subject permissions)

NATS authorization maps certificate identity (CN/SAN) to subject permissions,
enforcing which processes can publish and subscribe to which topics. Use the
certificate's Common Name (CN) to identify processes:

```nats
# nats-server configuration
authorization: {
  users: [
    # API instances: consume signing replies, publish to listen/resolve topics
    {
      user: "api-instance-1"
      permissions: {
        publish: ["certrequest.signed", "certrequest.wait.>"]
        subscribe: ["certrequest.signed", "certrequest.wait.>"]
      }
    },
    # Signer: consume signing requests, publish replies
    {
      user: "signer-instance"
      permissions: {
        publish: ["certrequest.signed"]
        subscribe: ["certrequest.sign"]
      }
    },
  ]
}
```

The subject layout:
- **`certrequest.sign`** — signing requests (queue group "signer"; exactly one consumer receives each)
- **`certrequest.signed`** — signed certificates ready to deliver (queue group "signed-listeners"; exactly one consumer per listener group)
- **`certrequest.wait.<request-id>`** — per-request delivery topics (fan-out; each client subscribed gets a copy)

API instances should have permission to both publish and subscribe (they are
both subscribers to the signed topic and publishers to the wait topics for
their own clients). Signer instances only need to consume the sign queue and
publish to the signed topic.

### Configuring ssoosshd for multi-instance

Set these in `/etc/ssoossh/ssoosshd.yaml` (or your config file):

```yaml
# Enable multi-instance mode: requires explicit session cookie key
multi_instance: true

# Session cookie must be consistent across instances
http:
  cookie_key: "your-secret-key-here-32-bytes-minimum"

# Configure NATS transport
pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/path/to/client-cert.pem"
    key_file: "/path/to/client-key.pem"
    ca_file: "/path/to/ca-cert.pem"

# All instances must use the same PostgreSQL database
db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"
```

Each instance receives its own client certificate. Update `cert_file`,
`key_file`, and `ca_file` paths on each instance if they differ.

### Failover and load balancing

Place instances behind a load balancer (haproxy, nginx, cloud LB). The load
balancer can route requests to any instance:

- Approvals on instance A reach a database that instance B reads
- Certificates issued on instance A reach clients waiting on instance B via
  NATS
- Sessions persist across instances (shared database, shared cookie key)

No sticky sessions required. If an instance crashes or NATS is unreachable:

- **Pending approvals**: Clients waiting in `Wait()` keep waiting on the database
  status. If the database status is still `pending` or `signing`, the wait loop
  continues. If it moves to `approved` and the wake message is lost, the client
  sees 410 Gone (certificate never persisted, by design) and must re-request.
- **NATS messages**: "Sign now" messages on the sign queue might be lost if a
  signer crashes mid-processing. Clients see the status stayed `signing` and
  keep waiting. After `request_ttl` elapses, the status expires and they re-request.
- **Session cookies**: Persist as long as the shared database is available and
  the `cookie_key` is consistent across instances. Instance restart doesn't affect sessions.

Clients never notice which instance they reached.

### What can go wrong

**Missing mTLS credentials:** `ssoosshd` fails at startup if `pubsub.backend: nats`
but any of `cert_file`, `key_file`, or `ca_file` is unset or unreadable.

**Wrong cookie_key:** If `multi_instance: true` but `http.cookie_key` is
unset, `ssoosshd` fails at startup. If `cookie_key` differs between instances,
users experience random logouts as requests land on instances with different
keys.

**Database connection issues:** One instance becoming unable to reach the
database does not affect others, but that instance becomes unavailable. The
database itself must be highly available (replication, failover).

**NATS availability:** If NATS is down, all instances can still serve cached
content (past session data, historical certificate records), but new approvals
cannot be delivered to waiting clients. Both the signer and listener(s) must
reach NATS for the certificate pipeline to work.

See [multi-instance-safety-plan.md](multi-instance-safety-plan.md) for design
details and [signer-split-deferred.md](signer-split-deferred.md) for how NATS
enables running the signer in a separate process.

## 8. PAM: `sudo` and `su`

New for this release. `docs/pam.d-sudo.example` documents every module
argument in detail (`server`, `trusted-ca-file`, `debug`,
`insecure-skip-verify`, `skew-tolerance`, `timeout`, `principals-map`) —
read it before editing a real PAM stack. This section is the operational
wrapper around it.

### The lockout warning

**Read this before editing `/etc/pam.d/sudo`.** Getting this file wrong
costs you `sudo` on that machine — not just PAM login, `sudo` specifically,
which is also how you'd normally fix a PAM mistake.

- Keep a root shell open in a second terminal (or an active `sudo` session)
  before you touch `/etc/pam.d/sudo`. Do not close it until you've
  confirmed `sudo` still works from a fresh terminal.
- Test in that second terminal, not the one you're editing from.
- Know how to revert: keep a copy of the working file
  (`cp /etc/pam.d/sudo /etc/pam.d/sudo.bak`) before editing, and know that
  restoring it from the still-open root shell is the recovery path if
  something is wrong.

### Stack configuration

Add the line from `docs/pam.d-sudo.example` to the `auth` group in
`/etc/pam.d/sudo` (and `/etc/pam.d/su`, if wanted), above the existing
`pam_unix.so` line:

```
auth  sufficient  pam_ssoossh.so  server=https://ssoosshd.example.com  trusted-ca-file=/etc/ssoossh/ca.pub
auth  sufficient  pam_unix.so  ...     # existing line, unchanged, stays below
```

`trusted-ca-file` can be the same `/etc/ssh/ca.pub` fetched in §3 — it's
the same authorized_keys-format CA public key either way.

### What happens when the server is unreachable

This is a control-flag decision, not just a module setting. The module itself fails fast on a
genuinely unreachable server (connection refused, DNS failure) rather than
hanging — it returns `PAM_AUTHINFO_UNAVAIL`, distinct from a timed-out or
denied approval. What happens next depends on the control flag chosen
above:

- **`sufficient`** (the example, and the recommended default): PAM falls
  through to the next module in the stack — typically `pam_unix.so`, so a
  local password still works. An outage of the ssoossh server degrades to
  "no browser approval available," not "no `sudo` at all."
- **`required`**: the whole `auth` group fails when `ssoosshd` is
  unreachable, and nothing later in the stack gets a chance to succeed —
  an outage of the ssoossh server becomes an outage of `sudo` on every host
  using it. Choose this only if that trade-off is genuinely wanted (e.g.
  password `sudo` is explicitly disallowed for compliance reasons).

### Clock synchronization

The server issues PAM certificates with a short `valid_duration`
(seconds — see `cert_options.pam` in `ssoosshd.yaml`), and the module
applies `skew-tolerance` (default 2s) symmetrically around that window.
If the sudo target's clock has drifted more than that tolerance from the
server's, valid approvals start failing certificate validity checks
intermittently — the kind of failure that looks like a bug and is
actually NTP not running. Run `chronyd`/`systemd-timesyncd` (or equivalent)
on every host running `pam_ssoossh`, and raise `skew-tolerance` only as a
last resort, since it widens the window a stolen certificate would remain
usable in.
