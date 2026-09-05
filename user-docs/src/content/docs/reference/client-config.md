---
title: ssoossh.yaml reference
description: Every key the ssoossh client reads, with its type, default, and effect.
eyebrow: Reference
sidebar:
  order: 1
---

Every setting in the client's `ssoossh.yaml`, grouped as the file itself groups
them. Only [`server`](#server) has no working default, so a usable file can be
one line.

For where the file is looked for, how several of them combine, and which source
wins, see [Client configuration](/ssoossh/guides/client-config/). The same
content ships as the `ssoossh.yaml(5)` man page and as the annotated
`/etc/ssoossh/ssoossh.yaml` installed by the `.deb` and `.rpm` packages.

## Server and trust

| Key | Type | Default |
| --- | --- | --- |
| [`server`](#server) | string | `empty` |
| [`capubkey`](#capubkey) | string | `empty` |
| [`insecure_skip_verify`](#insecure_skip_verify) | bool | `false` |

### `server`

`string`, default `empty`

Base URL of the ssoosshd server, including the scheme. If the scheme is
omitted, `https` is assumed. There is no default, and every command that talks
to the server fails without it. `--server` overrides it for one invocation.

```yaml
server: "https://ssh.example.com"
```

### `capubkey`

`string`, default `empty`

The server's CA public key, in `authorized_keys` format. When set, the client
skips fetching the CA key from the server at startup, which pins the key rather
than trusting a fetched one. When empty, it is fetched from the server on first
use.

```yaml
capubkey: "ecdsa-sha2-nistp384 AAAAE2VjZHNh... ssoossh-ca"
```

### `insecure_skip_verify`

`bool`, default `false`

Skip TLS certificate verification when talking to `server`. For development
against a self-signed certificate only; it defeats the point of pinning
`capubkey` above.

```yaml
insecure_skip_verify: false
```

## Key storage

| Key | Type | Default |
| --- | --- | --- |
| [`use_agent`](#use_agent) | bool | `true` |
| [`fallback_file_agent`](#fallback_file_agent) | bool | `true` |
| [`key_filename`](#key_filename) | string | `id_ssoossh` |

### `use_agent`

`bool`, default `true`

Keep keys and certificates in a running ssh-agent rather than in files. This is
the recommended mode, and the only mode `ssoossh ssh proxycommand` supports:
`ssh` reads key files once at startup and never sees a certificate written
after that.

Setting it to false means the agent is not consulted at all, not merely
deprioritized -- keys and certificates always go to the files named by
`key_filename`.

```yaml
use_agent: true
```

### `fallback_file_agent`

`bool`, default `true`

What to do when an agent was wanted but none could be reached: fall back to key
files (`true`), or fail the command (`false`). It has no effect when
`use_agent` is false, since file storage is already in use in that case.

`--debug` reports both the preference and the backend that actually resolved,
so a `use_agent: true` that ended up on files is visible rather than silent.

```yaml
fallback_file_agent: true
```

### `key_filename`

`string`, default `id_ssoossh`

Base name (or path) for the key and certificate files, used when `use_agent` is
false or when the fallback above took effect. A bare name resolves inside
`~/.ssh`, a `~/...` path is expanded, and an absolute path is used as given.
The public key and certificate are written beside it as `<name>.pub` and
`<name>-cert.pub`.

```yaml
key_filename: id_ssoossh
```

## Key generation

A fresh keypair is generated for every certificate request, so this is not a
long-lived identity file. Both `sshkey` sub-keys are optional.

| Key | Type | Default |
| --- | --- | --- |
| [`sshkey.type`](#sshkeytype) | string | `ecdsa` |
| [`sshkey.size`](#sshkeysize) | int | `0` (per-algorithm default) |
| [`fips`](#fips) | bool | `unset` |

### `sshkey.type`

`string`, default `ecdsa`

The key algorithm: `ed25519`, `ecdsa`, or `rsa`. The default is ECDSA P-384
unconditionally, whether or not FIPS steering is in effect. `--key-type`
overrides it for `ssoossh ssh login`.

`ed25519` stays selectable but is not the default: it only entered FIPS 186-5
in 2023 and several FIPS policies still reject it outright. While FIPS steering
is in effect, choosing an algorithm that is not FIPS-approved (`ecdsa` and
`rsa` are the approved ones) is a hard error at config-load time, not a
warning. The way around it is to disable `fips`, not to pick a different type
while it stays on.

```yaml
sshkey:
  type: ecdsa
```

### `sshkey.size`

`int`, default `0` (per-algorithm default)

What this means depends on `type`:

| `type` | `size` means | Accepted | Default |
| --- | --- | --- | --- |
| `ed25519` | nothing; it has one size | -- | ignored, and a nonzero value produces a warning |
| `ecdsa` | the NIST curve | 256, 384, 521 | 384 |
| `rsa` | the modulus in bits | at least 2048 | 3072 |

RSA key generation costs on the order of hundreds of milliseconds and is highly
variable; ecdsa and ed25519 are effectively instant. Since a fresh key is
generated on every certificate request, that cost is paid every time rather
than once. `--key-size` overrides this for `ssoossh ssh login`.

```yaml
sshkey:
  size: 384
```

### `fips`

`bool`, default `unset`

Steers key generation toward algorithms that SSH implementations running in
FIPS mode accept. When in effect, a non-approved `sshkey.type` is a hard error
at config-load time.

Left unset deliberately, in which case it follows whether the running binary is
itself in Go's FIPS 140-3 mode. Set it explicitly to target a FIPS-mode server
from a client that is not itself in FIPS mode, or to `false` as the escape
hatch -- unless a system `enforce` file locks `fips: true`, which wins
regardless.

This setting only affects the client's own algorithm checks. Unlike the server,
there is no per-install hook here that also sets `GODEBUG=fips140=only` in the
binary's environment; an operator wanting runtime-layer enforcement beneath
these checks sets it themselves, in the shell profile or wrapper that invokes
`ssoossh`.

```yaml
fips: true
```

## Certificate extensions

| Key | Type | Default |
| --- | --- | --- |
| [`certificate_extensions.no_pty`](#certificate_extensionsno_pty) | bool | `false` |
| [`certificate_extensions.no_agent_forwarding`](#certificate_extensionsno_agent_forwarding) | bool | `false` |
| [`certificate_extensions.no_port_forwarding`](#certificate_extensionsno_port_forwarding) | bool | `false` |
| [`certificate_extensions.no_x11_forwarding`](#certificate_extensionsno_x11_forwarding) | bool | `false` |
| [`certificate_extensions.no_user_rc`](#certificate_extensionsno_user_rc) | bool | `false` |

### `certificate_extensions`

`ssoossh ssh login` requests the full interactive extension set by default:
`permit-pty`, `permit-agent-forwarding`, `permit-port-forwarding`,
`permit-X11-forwarding`, and `permit-user-rc`. Each key below is an opt-out
from that set: `false` (the default) requests the extension, `true` does not.

The effective set is the default, minus these config opt-outs, minus the
matching `--no-*` flags, minus anything platform-native policy forbids -- in
that order. A flag cannot re-add what policy forbids.

Requesting the full set is always safe: the server narrows the request against
its own configuration before signing, and the approval page shows what
survived. Requesting nothing is not useful, because narrowing is an
intersection -- an empty request yields a certificate carrying no extensions at
all, which cannot open an interactive session.

```yaml
certificate_extensions:
  no_pty: false
  no_agent_forwarding: false
  no_port_forwarding: false
  no_x11_forwarding: false
  no_user_rc: false
```

### `certificate_extensions.no_pty`

`bool`, default `false`

Do not request `permit-pty`. Without that extension the certificate cannot open
an interactive terminal. Overridden by `--no-pty`.

```yaml
certificate_extensions:
  no_pty: false
```

### `certificate_extensions.no_agent_forwarding`

`bool`, default `false`

Do not request `permit-agent-forwarding`. Overridden by
`--no-agent-forwarding`.

```yaml
certificate_extensions:
  no_agent_forwarding: false
```

### `certificate_extensions.no_port_forwarding`

`bool`, default `false`

Do not request `permit-port-forwarding`. Overridden by `--no-port-forwarding`.

```yaml
certificate_extensions:
  no_port_forwarding: false
```

### `certificate_extensions.no_x11_forwarding`

`bool`, default `false`

Do not request `permit-X11-forwarding`. Overridden by `--no-x11-forwarding`.

```yaml
certificate_extensions:
  no_x11_forwarding: false
```

### `certificate_extensions.no_user_rc`

`bool`, default `false`

Do not request `permit-user-rc`, which is what lets `~/.ssh/rc` run on login.
Overridden by `--no-user-rc`.

```yaml
certificate_extensions:
  no_user_rc: false
```

## Browser, administration, and reserved keys

| Key | Type | Default |
| --- | --- | --- |
| [`try_open_browser`](#try_open_browser) | bool | `false` |
| [`enforce`](#enforce) | string | `empty` |
| [`username`](#username) | string | `empty` |

### `try_open_browser`

`bool`, default `false`

Make a best-effort attempt to launch the system browser at the approval URL
during `ssoossh ssh login`. Strictly best-effort: the URL is always printed
first, so a headless machine with no browser can still complete a login by
hand, and a launch failure is reported and otherwise ignored.

```yaml
try_open_browser: true
```

### `enforce`

`string`, default `empty`

Names another YAML file whose settings a user cannot override: it is merged
after the per-user file, the local file, and `--config`.

Only honored when read from the machine-wide file
(`/etc/ssoossh/ssoossh.yaml`, or `%ProgramData%\ssoossh\ssoossh.yaml` on
Windows). A bare filename resolves next to that file, so naming one cannot
reach a file the user controls. Setting `enforce` anywhere else has no special
effect. A missing or malformed target is a hard startup error, unlike the
optional files above.

A guardrail, not a security boundary: the client runs as the user, who can
always supply their own binary. The one limit enforced beyond client
cooperation is `cert_options.*.valid_duration` on the server. Windows and macOS
can lock the same settings through Group Policy and managed preferences instead
-- see [Client settings enforcement](/ssoossh/hosts/client-enforcement/).

```yaml
# /etc/ssoossh/ssoossh.yaml only
enforce: ssoossh-locked.yaml
```

:::caution[Windows]
The `%ProgramData%\ssoossh` directory must be created by an installer running
as administrator. ProgramData's default permissions let any user create a
subdirectory, and whoever creates it owns it -- so a non-administrator who gets
there first would own the file that is supposed to constrain them.
:::

### `username`

`string`, default `empty`

Present in the schema and read by nothing. It was intended to name a service
account, but that is chosen by the approver in the web UI instead: enrollment
requests are unauthenticated, so the client has no session against which to
validate one. Setting it changes nothing.

```yaml
# username: has no effect
```
