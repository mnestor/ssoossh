---
title: Installing the server
description: Packages, the CA key, the systemd unit, the minimum config, and the first health check.
eyebrow: Server operations
sidebar:
  order: 1
---

Getting one `ssoosshd` process running on a Linux host. Everything here is
the same whether you end up running one instance or twenty; the split-process
and multi-instance work comes later.

## Packages

The server ships as a `ssoosshd` package. The `.deb` and `.rpm` builds are
glibc; the `.apk` build is musl, for Alpine. Both are dynamically linked so a
PKCS#11 module can be loaded at runtime (see
[HSM and PKCS#11](/ssoossh/operations/hsm/)), and both are built for
linux/amd64 and linux/arm64.

| What | Where it lands |
| --- | --- |
| the binary | `/usr/local/sbin/ssoosshd` |
| the annotated config sample | `/etc/ssoossh/ssoosshd.yaml` |
| man pages | `ssoosshd(8)`, `ssoosshd-serve(8)`, `ssoosshd-sign(8)`, `ssoosshd.yaml(5)` |
| reference mail templates | `/usr/share/ssoossh/mail-templates/` |

The config sample is installed as a no-replace config file, so an upgrade
never overwrites your edits. It is generated from the server's config structs,
which makes it the same content as `ssoosshd.yaml(5)` and the
[configuration reference](/ssoossh/reference/config/) on this site.

There are also container images, `ghcr.io/mnestor/ssoosshd:<version>` (glibc,
distroless) and `ghcr.io/mnestor/ssoosshd:<version>-musl` (Alpine). No
floating tag is published: pin an explicit released version, because a
floating tag would let a restart silently change what is running. A Compose
deployment ships at
[deploy/docker-compose.yml](https://github.com/mnestor/ssoossh/blob/main/deploy/docker-compose.yml).

:::note
In a container, set [`http.address`](/ssoossh/reference/config/http/#address)
to `0.0.0.0`. The default is `127.0.0.1`, which a published port cannot reach.
:::

## The CA key

`ssoosshd` signs with an SSH CA keypair. Generate one:

```bash
ssh-keygen -t ed25519 -f ca -C "ssoossh CA" -N ""
```

That writes `ca` (private) and `ca.pub` (public). The private key goes into
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key) as inline PEM, not
as a file path, using a YAML literal block:

```yaml
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  ...
  -----END OPENSSH PRIVATE KEY-----
```

Once it is in the config file, delete the plaintext `ca` file or move it
somewhere access-controlled.

:::danger
Anyone who can read `ssoosshd.yaml` can sign certificates as this CA. Treat
the file as the private key it contains: owned by the service account, mode
`0640` at the loosest, and never in a configuration-management repository in
plaintext.
:::

Exactly one of `ssh_key` or [`hsm`](/ssoossh/reference/config/hsm/) may be
set, and a signing process needs one of them: startup fails with neither. To
keep the private key out of the process entirely, source it from a PKCS#11
token ([HSM and PKCS#11](/ssoossh/operations/hsm/)) or split the signer into
its own process ([Startup modes](/ssoossh/operations/startup-modes/)) -- or
both.

Ed25519 is a good default for `ssh_key`. It is the one algorithm the PKCS#11
path does not support, so choose ECDSA or RSA if you intend to move the key to
a token later.

## The systemd unit

The package installs the binary and the config sample. It does not install a
systemd unit and does not create a service account; do both by hand:

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin ssoossh
install -o ssoossh -g ssoossh -m 0750 -d /var/lib/ssoossh
chown -R ssoossh:ssoossh /etc/ssoossh
cp deploy/ssoosshd.service /etc/systemd/system/ssoosshd.service
systemctl daemon-reload
systemctl enable --now ssoosshd
```

The shipped unit is
[deploy/ssoosshd.service](https://github.com/mnestor/ssoossh/blob/main/deploy/ssoosshd.service).
What it does, and why:

| Directive | Why |
| --- | --- |
| `User=ssoossh`, `Group=ssoossh` | a dedicated unprivileged account rather than root or `DynamicUser`, because the identity has to be stable enough to `chown` the config and data directories to it ahead of time |
| `StateDirectory=ssoossh`, mode `0750` | `/var/lib/ssoossh`, where the default SQLite file belongs |
| `LogsDirectory=ssoossh`, mode `0750` | `/var/log/ssoossh`, for any `logging.filename` you set rather than leaving output to journald |
| `ReadOnlyPaths=/etc/ssoossh` | the config directory holds the CA private key and is never written by the service |
| `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `ProtectKernelTunables`, `ProtectKernelModules`, `ProtectControlGroups`, `RestrictSUIDSGID`, `RestrictRealtime`, `LockPersonality`, `MemoryDenyWriteExecute` | the sandbox |
| `Restart=on-failure`, `RestartSec=5s` | a config error fails at startup, so a crash loop here is a signal, not a nuisance |

The unit also carries a commented `Environment=GODEBUG=fips140=only` line. It
is left commented because it would enforce FIPS on every server that installs
the unit, regardless of whether that deployment sets
[`fips`](/ssoossh/reference/config/top-level/#fips).

Point [`db.connection_string`](/ssoossh/reference/config/db/#connection_string)
at a path inside the state directory so the SQLite file persists across
restarts:

```yaml
db:
  connection_string: /var/lib/ssoossh/ssoossh.db
```

## The minimum config

A CA key, a public URL, and an OIDC client. Everything else has a working
default.

```yaml
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  -----END OPENSSH PRIVATE KEY-----

http:
  # The scheme and host browsers actually reach this deployment at.
  public_url: "https://ssh.example.com"

authentication:
  client_id: "..."
  client_secret: "..."
  provider_url: "https://idp.example.com"
```

[`http.public_url`](/ssoossh/reference/config/http/#public_url) is required
and is an origin only -- a path, query, or fragment is rejected at startup.
Four things derive from it: the OIDC redirect URI, the origin the CSRF
middleware compares against, the host name requests must be addressed to
(anything else gets `421 Misdirected Request`), and whether the deployment is
HTTPS at all, which decides the session cookie's `Secure` attribute.

More complete starting points, including TLS, PostgreSQL, and multi-instance:
[Server configuration examples](/ssoossh/examples/server-configs/).

## First start and health check

```bash
systemctl status ssoosshd
journalctl -u ssoosshd -f
```

`ssoosshd` validates its whole configuration at startup and refuses to run on
a bad one rather than failing at the first request. A malformed key ID
template, an unparseable CIDR in a source policy, a mail template override
that does not compile, an unreadable SMTP password file, a lifetime policy
with no `default_duration`, `multi_instance: true` with no `http.cookie_key`
-- all of these stop the process with a message naming the setting.

Two liveness endpoints answer without authentication, and both are registered
ahead of the host-name check so a probe addressing the server by IP still
reaches them:

```bash
curl -f http://localhost:8080/healthz   # {"status":"ok"}
curl -f http://localhost:8080/ping      # pong
```

Then confirm the CA key the running server actually holds, rather than
trusting the file you generated:

```bash
ssoossh --server https://ssh.example.com ca
```

That is the same key to install on your hosts as `TrustedUserCAKeys` --
[Trusting the CA in sshd](/ssoossh/hosts/sshd-trust/).

:::note
The default log level is `WARN`, so a healthy server is quiet. Set
[`logging.level`](/ssoossh/reference/config/logging/#level) to `info` while
you are bringing a deployment up. The HTTP access log has its own threshold,
[`http.access_logging.level`](/ssoossh/reference/config/http/access_logging/#level),
filtered independently and shipped at `info`, so requests are logged even
while the application log stays at `WARN`. Leave that key empty and the
access log falls back to `logging.level`, where the shipped `WARN` means no
successful request is ever recorded.
:::

## Next

[Point it at your identity provider](/ssoossh/operations/identity-provider/).
Until an OIDC client exists, nobody can log in to approve anything.
