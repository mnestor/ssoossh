# Deployment

Operator-facing runbook for bringing up `ssoosshd`, wiring `sshd` and
`sudo`/`su` to trust it, and pointing it at an OIDC provider.

If you just want the shortest path to a working login, start with
[getting-started.md](../guide/getting-started.md). For what each setting means,
across every configuration surface, see
[configuration.md](configuration.md); this document is the procedures
around those settings.

Two ways to run `ssoosshd`: the systemd unit below (installed from the
`.deb`/`.rpm` package), or the Docker Compose deployment at
[../deploy/docker-compose.yml](../../deploy/docker-compose.yml). Both need
the same configuration; this doc mostly matters regardless of which one
you run.

## 1. CA key

`ssoosshd` signs with an SSH CA keypair. Generate one and paste the
private key into `ssh_key` in `ssoosshd.yaml`; it's inline PEM, not a file
path (see [configuration.md](configuration.md#server-ssoosshdyaml)):

```sh
ssh-keygen -t ed25519 -f ca -C "ssoossh CA" -N ""
```

This writes `ca` (private key) and `ca.pub` (public key). Put `ca`'s
contents under `ssh_key:` as a literal block scalar (`ssh_key: |` followed
by the indented key), then treat the plaintext file as sensitive: delete
it or move it somewhere access-controlled once it's in the config, since
anyone who can read `ssoosshd.yaml` can sign certificates as this CA.

## 2. systemd

The `ssoosshd` package (`.deb`/`.rpm`) installs the binary at
`/usr/local/sbin/ssoosshd` and the annotated config sample at
`/etc/ssoossh/ssoosshd.yaml`. It does not currently install a systemd unit
or create a service account; do both by hand:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin ssoossh
install -o ssoossh -g ssoossh -m 0750 -d /var/lib/ssoossh
chown -R ssoossh:ssoossh /etc/ssoossh
cp deploy/ssoosshd.service /etc/systemd/system/ssoosshd.service
systemctl daemon-reload
systemctl enable --now ssoosshd
```

See [../deploy/ssoosshd.service](../../deploy/ssoosshd.service) for what the
unit actually sandboxes (`ReadOnlyPaths=/etc/ssoossh`,
`StateDirectory=ssoossh` for the default sqlite database file, etc.); the
comments there explain each choice, not repeated here.

Set `db.connection_string` in `ssoosshd.yaml` to a path under
`/var/lib/ssoossh/` (e.g. `/var/lib/ssoossh/ssoossh.db`) so the sqlite
file lands in the directory the unit already owns and persists across
restarts.

## 3. sshd: TrustedUserCAKeys

Once `ssoosshd` is running, fetch its CA public key with the client (the
recommended way: it confirms the running server's key matches what you
loaded, rather than trusting the file from step 1 blindly):

```sh
ssoossh --server https://ssh.example.com ca > /etc/ssh/ca.pub
```

Then on every host that should accept certificates from this CA, in
`/etc/ssh/sshd_config`:

```
TrustedUserCAKeys /etc/ssh/ca.pub
```

and `systemctl reload sshd`.

Client-side `ssh_config` recipes (`Match exec` vs `ProxyCommand`) are in
[configuration.md](configuration.md#ssh_config).

## 4. OIDC provider setup (pocket-id)

[pocket-id](https://github.com/pocket-id/pocket-id) is the reference
provider: homelab-friendly, and what the project's own reference config
assumes. Any OIDC-compliant provider works the same way.

1. Run pocket-id (its own container or binary) and complete its first-run
   admin setup.
2. In pocket-id's admin UI, create an OIDC client for ssoossh. Set its
   callback URL to `<http.public_url>/auth/callback`; this is the one
   redirect URI the client needs. Note the generated client ID and secret.
3. Create (or import) the user(s) who will log in, and any groups needed
   for `cert_options.*.require` gating (e.g. an "SSH Sudoers" group
   for PAM; see §8 below).
4. In `ssoosshd.yaml`'s `authentication` section, set `provider_url` to
   pocket-id's base URL and `client_id`/`client_secret` to the values from
   step 2. Leave `fields.username` at the default `preferred_username` and
   set `fields.groups` if group-gated certificate types are in use.
5. Restart `ssoosshd` and confirm
   `GET <provider_url>/.well-known/openid-configuration` resolves from the
   server; this is the first thing that fails on a typo'd `provider_url`.

## 5. Reverse proxy and TLS

Terminating TLS in front of `ssoosshd` (nginx, Caddy, Traefik, etc.)
rather than in the process itself is the common case. Two settings matter
and are easy to get wrong because the failure is silent until someone
tries to log in: `http.public_url` and `http.trusted_proxies`. What each
does, and the direct-TLS alternative:
[configuration.md](configuration.md#tls).

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

Set `http.trusted_proxies` to the proxy's address (e.g.
`["127.0.0.1/32"]`); otherwise `X-Forwarded-For` is ignored and every
request is attributed to the proxy's own IP, including in the approver
client-IP key ID field.

## 6. Startup modes: full, api, and sign

`ssoosshd` can run in three modes, selected by subcommand. The
configuration each requires is summarized in
[configuration.md](configuration.md#multi-instance-and-startup-modes).

**Full mode (default): `ssoosshd serve`** (or explicitly
`ssoosshd serve full`). All components in one process: HTTP server,
certificate approval listener, in-process signer holding the CA key. For
single-instance deployments, development, and testing. Pubsub defaults to
the in-process backend; no additional setup.

**API-only mode: `ssoosshd serve api`.** HTTP server and listener without
the signer; signing jobs are published to NATS. No CA key and no database
access for signing. For multi-instance deployments where the signer is
isolated. Fails at startup if the in-process pubsub backend is configured
(signing jobs would go nowhere).

**Signer-only mode: `ssoosshd sign`.** Only the signing component: a NATS
connection and the CA key. Consumes signing jobs, publishes signed
certificates. Does not need or use database, HTTP, OIDC, or LDAP
settings. Run it on a separate machine with restricted network access to
isolate the CA key. Fails at startup if the in-process backend is
configured (it cannot bridge separate processes).

Example systemd drop-ins:

```ini
[Service]
ExecStart=/usr/local/sbin/ssoosshd serve api
```

```ini
[Service]
ExecStart=/usr/local/sbin/ssoosshd sign
```

## 7. Running more than one instance

Multi-instance deployment means several `ssoosshd serve api` instances
(HTTP + listener) behind a load balancer, one or more `ssoosshd sign`
processes, all connected to NATS and sharing a PostgreSQL database.

**Requires:**

- A shared PostgreSQL database (SQLite is single-connection only)
- NATS as the message broker (see §6 for mode options)
- mTLS client certificates for NATS authentication
- An explicit session cookie key (not per-process random)
- Multi-instance mode enabled (`multi_instance: true`)

The `ssoosshd.yaml` settings for all of this are in
[configuration.md](configuration.md#multi-instance-and-startup-modes). If
you only run one process, skip this section and use the default full mode.

### Setting up NATS

NATS carries certificate approvals, signatures, and delivery
notifications across instances. Each instance must reach the NATS server
over the network.

**Development:** `docker run -p 4222:4222 nats:latest` (port 4222, no
authentication; not suitable for production).

**Production:** NATS requires mutual TLS. Generate or obtain a NATS
server certificate and key, a CA certificate to verify the server, and a
client certificate and key for each `ssoosshd` instance. With a local CA:

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

### NATS authorization (subject permissions)

NATS authorization maps certificate identity (CN/SAN) to subject
permissions, enforcing which processes can publish and subscribe to which
topics:

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

- **`certrequest.sign`**: signing requests (queue group "signer"; exactly
  one consumer receives each)
- **`certrequest.signed`**: signed certificates ready to deliver (queue
  group "signed-listeners"; exactly one consumer per listener group)
- **`certrequest.wait.<request-id>`**: per-request delivery topics
  (fan-out; each subscribed client gets a copy)

API instances need publish and subscribe on the signed and wait topics
(they subscribe to the signed topic and publish to wait topics for their
own clients). Signer instances only consume the sign queue and publish to
the signed topic.

### Failover and load balancing

Place instances behind a load balancer (haproxy, nginx, cloud LB). The
load balancer can route requests to any instance:

- Approvals on instance A reach a database that instance B reads
- Certificates issued on instance A reach clients waiting on instance B
  via NATS
- Sessions persist across instances (shared database, shared cookie key)

No sticky sessions required. If an instance crashes or NATS is
unreachable:

- **Pending approvals**: clients keep waiting on the database status. If
  it is still `pending` or `signing`, the wait loop continues. If it moves
  to `approved` and the wake message is lost, the client sees 410 Gone
  (the certificate is never persisted, by design) and must re-request.
- **NATS messages**: a "sign now" message might be lost if a signer
  crashes mid-processing. Clients see the status stay `signing` and keep
  waiting; after `client_timeout` elapses the stranded-request sweep
  fails it and they re-request.
- **Session cookies**: persist as long as the shared database is available
  and `cookie_key` is consistent across instances. Instance restart does
  not affect sessions.

Clients never notice which instance they reached.

### What can go wrong

- **Missing mTLS credentials:** `ssoosshd` fails at startup if
  `pubsub.backend: nats` but any of `cert_file`, `key_file`, or `ca_file`
  is unset or unreadable.
- **Wrong cookie_key:** if `multi_instance: true` but `http.cookie_key` is
  unset, `ssoosshd` fails at startup. If `cookie_key` differs between
  instances, users experience random logouts as requests land on
  instances with different keys.
- **Database connection issues:** one instance losing the database does
  not affect others, but that instance becomes unavailable. The database
  itself must be highly available (replication, failover).
- **NATS availability:** if NATS is down, instances can still serve
  cached content (session data, historical certificate records), but new
  approvals cannot be delivered to waiting clients. Both the signer and
  the listeners must reach NATS for the certificate pipeline to work.

See [multi-instance-safety-plan.md](../dev/multi-instance-safety-plan.md) for
design details and [signer-split-deferred.md](../dev/signer-split-deferred.md)
for how NATS enables running the signer in a separate process.

## 8. PAM: `sudo` and `su`

[pam.d-sudo.example](../pam.d-sudo.example) documents every module argument
in detail (`server`, `trusted-ca-file`, `debug`, `insecure-skip-verify`,
`skew-tolerance`, `timeout`, `principals-map`); read it before editing a
real PAM stack. This section is the operational wrapper around it.

### The lockout warning

**Read this before editing `/etc/pam.d/sudo`.** Getting this file wrong
costs you `sudo` on that machine. Not just PAM login: `sudo` specifically,
which is also how you'd normally fix a PAM mistake.

- Keep a root shell open in a second terminal (or an active `sudo`
  session) before you touch `/etc/pam.d/sudo`. Do not close it until
  you've confirmed `sudo` still works from a fresh terminal.
- Test in that second terminal, not the one you're editing from.
- Know how to revert: keep a copy of the working file
  (`cp /etc/pam.d/sudo /etc/pam.d/sudo.bak`) before editing, and know
  that restoring it from the still-open root shell is the recovery path.

### Stack configuration

Add the line from [pam.d-sudo.example](../pam.d-sudo.example) to the `auth`
group in `/etc/pam.d/sudo` (and `/etc/pam.d/su`, if wanted), above the
existing `pam_unix.so` line:

```
auth  sufficient  pam_ssoossh.so  server=https://ssoosshd.example.com  trusted-ca-file=/etc/ssoossh/ca.pub
auth  sufficient  pam_unix.so  ...     # existing line, unchanged, stays below
```

### Who the certificate names, and why you probably need `principals-map`

An issued PAM certificate carries the **approver's** OIDC username and the
other accounts they hold. It does not carry the local account the module is
authenticating: that value comes from an unauthenticated client, so it is
shown to the approver and written to the audit record, and nothing else.

Check 3 then asks the host whether those principals authorize the local
account. With no `principals-map`, that is an exact match, so the line above
works only where every user's OIDC username is identical to the account they
`sudo` as. Everywhere else, add the map:

```yaml
# /etc/ssoossh/principals.yaml
mnestor:
  - mike.nestor
  - mnestor
root:
  - mike.nestor      # only mike.nestor may sudo to root on this host
```

```
auth  sufficient  pam_ssoossh.so  server=...  trusted-ca-file=...  principals-map=/etc/ssoossh/principals.yaml
```

Two things to know before you turn it on:

- **A configured map is authoritative for every account.** An account with
  no entry in it is denied. List every account that should be able to
  authenticate through ssoossh, not just the ones that need a rename.
- **A map that fails to load falls back to the exact-match check**, logged
  at warning level on every attempt. That fallback is stricter than the map,
  so a typo'd path shows up as denied logins rather than as looser access.
  Grep for the warning if `sudo` starts refusing after a config change.

This is also the file that decides who may `sudo` to `root` on a given
machine, and only root on that machine can edit it. That is deliberate: it
keeps "this person may become root here" from being a statement about every
host that trusts the CA.

`trusted-ca-file` can be the same `/etc/ssh/ca.pub` fetched in §3; it's
the same authorized_keys-format CA public key either way.

### What happens when the server is unreachable

This is a control-flag decision, not just a module setting. The module
itself fails fast on a genuinely unreachable server (connection refused,
DNS failure) rather than hanging: it returns `PAM_AUTHINFO_UNAVAIL`,
distinct from a timed-out or denied approval. What happens next depends
on the control flag chosen above:

- **`sufficient`** (the example, and the recommended default): PAM falls
  through to the next module in the stack, typically `pam_unix.so`, so a
  local password still works. An outage of the ssoossh server degrades to
  "no browser approval available," not "no `sudo` at all."
- **`required`**: the whole `auth` group fails when `ssoosshd` is
  unreachable, and nothing later in the stack gets a chance to succeed.
  An outage of the ssoossh server becomes an outage of `sudo` on every
  host using it. Choose this only if that trade-off is genuinely wanted
  (e.g. password `sudo` is explicitly disallowed for compliance reasons).

### Clock synchronization

The server issues PAM certificates with a short `valid_duration` (seconds;
see `cert_options.pam` in `ssoosshd.yaml`), and the module applies
`skew-tolerance` (default 2s) symmetrically around that window. If the
sudo target's clock has drifted more than that tolerance from the
server's, valid approvals start failing certificate validity checks
intermittently: the kind of failure that looks like a bug and is actually
NTP not running. Run `chronyd`/`systemd-timesyncd` (or equivalent) on
every host running `pam_ssoossh`, and raise `skew-tolerance` only as a
last resort, since it widens the window a stolen certificate would remain
usable in.

---

## 9. Console login

A `console` certificate authorizes an interactive login at a machine with
no browser in front of it — a physical tty, a serial console, a BMC or KVM
viewer, a VM console. The approval travels as a short code the person at
the keyboard reads off the screen and types into the web UI.

Everything below is server side. The PAM module that drives a console is
shipped separately from this repo; `pam_ssoossh` does `sudo` and `su`
only, and must not be added to `/etc/pam.d/login`.

### Server configuration

```yaml
cert_options:
  console:
    # Who may approve a console login at all. This is deployment-wide, not
    # per host — see below for the per-host half.
    require:
      group: staff

    # Refuse a request from outside these networks, at creation, before a
    # keypair is certified and before any human is asked. Gated on the
    # address the server observes, not on the hostname the caller sent:
    # a host cannot prove its name, which is why there are no host
    # certificates either.
    #
    # Behind a reverse proxy this only means anything with
    # http.trusted_proxies set. Without it every request carries the
    # proxy's address and the gate either admits everything or nothing.
    allowed_networks:
      - 10.20.0.0/16      # the management VLAN
      - 192.168.50.0/24   # the lab

    # This type's whole budget, and the one setting worth thinking about.
    # It defaults to 2m against the global cert_options.client_timeout of
    # 5m, and a value longer than the global is a startup error.
    #
    # Short on purpose: the approval window is the attacker's working time
    # in the case the code exists to defend against — someone starts a
    # login at an unattended console and phones a colleague to read them
    # the code. Two minutes gives the approver 96 seconds, which is the
    # human's share after the signing reserve.
    #
    # There is a floor, and it is not the technical one. Below about 90s a
    # first approval that has to go through an OIDC sign-in starts failing,
    # people retry, and a flow people habitually retry is a flow they learn
    # to approve without reading.
    client_timeout: 2m

    # Validated once by the module and discarded, exactly like the PAM type.
    valid_duration: 30s

http:
  cert_request_rate_limit:
    console: 10           # per second, per source address
  console_code_rate_limit:
    limit: 1              # per second, per session AND per source address
    burst: 5
```

### Per-host policy belongs in the host's PAM stack

`cert_options.console.require` is one condition for the whole deployment.
In a fleet that degenerates: `web01` is the web team's, `db07` the DBAs',
`rack07-bmc` two people in facilities, and a single server-wide group has
to be the union of all of them, at which point it gates nothing.

The answer is not to send a group to the server. Put the gate in the
host's own stack, above the ssoossh line:

```
# /etc/pam.d/login
auth  [success=ignore default=die]  pam_succeed_if.so  user ingroup console-web01 quiet
auth  sufficient                    <the console module>  ...
auth  include                       common-auth
```

That gate is root-owned on the host, cannot be omitted by whoever is at
the keyboard, and fails before any keypair, request row, or network call.
A group field on the wire would be untrusted input from an unauthenticated
caller: it could only ever narrow, so nobody could use it to widen
anything, but they could **omit** it and fall back to the server-wide
condition — a control that silently stops applying exactly when someone is
attacking it, which is worse than no control at all.

`pam_access` with `/etc/security/access.conf` is the same argument if the
gate wants to be per-tty or per-origin instead of per-group.

### Lockout safety

The `sudo` warnings in §8 apply with more force here, because the failure
is at the physical console:

1. **`sufficient`, never `required` or `requisite`.** An ssoosshd outage
   must fall through to the local stack. A console behind an SSO that
   needs the network is a console that does not work when the network is
   the thing that is broken, and console is the break-glass path.
2. **Keep a working local credential**, and keep it somewhere physical.
3. **Never edit `/etc/pam.d/login` without a second root session open**,
   and verify from that session before closing it.
4. **Keep `root` out** unless deliberately enabled. Root console login is
   the recovery path that has to keep working when ssoosshd does not, so
   routing it through ssoosshd is usually a mistake.
5. **Screen lockers are the same stack.** `sddm`, `swaylock`,
   `xscreensaver` and friends authenticate through PAM, so adding a module
   to a shared `common-auth` puts screen unlock behind the network. Wire it
   per service, never into `common-auth`.
6. **Accounts must already exist.** ssoossh provisions nothing: the account
   has to resolve through NSS before `login` will offer it a PAM stack at
   all. Pair with `principals-map` for the identity-to-account mapping.

### What the approver sees, and what it is worth

The approval page shows the account being logged into, the hostname, the
PAM service, the tty, and any reported remote host — all of it
self-reported by an unauthenticated caller and labelled as such — plus the
source address the server observed and the time the request was made.

Its value is that it lets a human notice "I am at my desk, why is there a
console login on rack07". A console login that also reports a remote host
is flagged outright, because a real console has nobody connecting to it
over the network.

The certificate names the **approver's** accounts, not the account typed at
the `login:` prompt. Whether those principals authorize that account is the
host's decision, made against its own root-owned `principals-map`. So
someone who types `root` at an unattended console gets a certificate the
host refuses unless the map already says that approver may become root.

What no host-side gate constrains is *who approved*: one person can
approve a console login for another on a host where both accounts are
permitted, and nothing refuses it — that is also the legitimate case of an
operator unlocking a console for a colleague. The audit trail is the
control there. `cert.code_resolved` records who typed the code and which
machine they were told about, and the decision record carries their
subject, username, groups and source.
