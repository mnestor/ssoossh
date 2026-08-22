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

## 7. PAM: `sudo` and `su`

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
