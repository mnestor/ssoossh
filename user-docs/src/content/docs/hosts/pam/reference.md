---
title: pam_ssoossh reference
description: Every argument, mode, check, return value and log line of the PAM module, from its manual page.
eyebrow: Host administration
sidebar:
  order: 8
---

The authoritative page for the module's behaviour. It restates
`pam_ssoossh(8)`, which is what the module on your host does.

## Synopsis

```ini
auth sufficient pam_ssoossh.so server=URL trusted-ca-file=PATH [options]
```

`pam_ssoossh` authenticates a local account by generating a single-use SSH
keypair, asking `ssoosshd` to certify it, showing the person at the terminal
how to approve the request, waiting for a human to approve it, and validating
the certificate that comes back against a set of trusted certificate
authorities.

It implements the **`auth` stack only**, through `pam_sm_authenticate` and
`pam_sm_setcred`. The latter establishes nothing: the certificate is consumed
during authentication and never installed into the session. Do not list the
module on `account`, `password` or `session` lines; it has no code for them.

## Modes, and which flow runs

Two flows exist, and `mode=auto` picks between them for every login.

### The browser flow

On a machine with a browser, or in a session that came from one, the module
prints one message through the PAM conversation and waits:

```text
Approve this request in your browser:
https://ssoossh.example.com/approve/6f1c0a5e-1f2b-4c3d-8e9f-0a1b2c3d4e5f
```

Nothing is read back. The login proceeds when someone with the right to
approve it does so in the browser, or fails when they deny it, when `timeout`
elapses, or when the person at the terminal presses Ctrl-C.

### The console flow

On a machine with no browser in front of it -- a virtual console, a serial
line, a hypervisor or BMC console -- an approval URL is a link nobody can
follow. The console flow prints a short code and, where it fits, a QR code
carrying the complete verification URL, and the approver carries either to a
device that does have a browser:

```text
Approve this login from a device with a browser.

  Go to:  https://ssoossh.example.com/console
  Code:   K7M4-QP2X

  █████████████████████████████████
  ██ ▄▄▄▄▄ █▄ ▀ ▀▄▄ █▀ █▄█ ▄▄▄▄▄ ██
  ...
```

The code is Crockford base32 in two groups; the QR code is drawn from Unicode
half-block characters and needs a terminal font that has them. It is omitted,
and the code stands alone, when the message would not fit.

### Which flow runs

`mode=auto` chooses the console flow when `PAM_RHOST` is empty -- nothing came
in over the network -- **and** `PAM_TTY` names a physical terminal: a Linux or
FreeBSD virtual console, a serial line, a hypervisor console, or `/dev/console`
itself.

Everything else gets the browser flow, and each exclusion is deliberate:

| Condition | Flow | Reason |
| --- | --- | --- |
| A pseudo-terminal (`pts/N`) | Browser | A terminal emulator or an SSH session, where a browser is a keystroke away |
| `PAM_TTY` unset | Browser | A cron job or a script, with no human at all |
| `PAM_RHOST` set | Browser | The person is at their own machine, which has a browser, however console-like the terminal on this end looks |

The decision is therefore per login rather than per host: the same `pam.d` line
does the right thing for a serial console and for an SSH session.

## Options

Arguments are `key=value` words on the `pam.d` line. A value containing spaces
uses libpam's bracket form, `key=[a value]`.

| Option | Default | |
| --- | --- | --- |
| [`server=URL`](#serverurl) | -- | required; a missing scheme becomes `https://` |
| [`trusted-ca-file=PATH`](#trusted-ca-filepath) | -- | required; `authorized_keys` format, one CA per line |
| [`principals-map=PATH`](#principals-mappath) | unset | which principals may assume which account |
| [`skew-tolerance=DURATION`](#skew-toleranceduration) | `2s` | applied to both ends of the validity window |
| [`timeout=DURATION`](#timeoutduration) | `60s` | bounds the whole attempt |
| [`insecure-skip-verify`](#insecure-skip-verify) | off | skips TLS verification; logs a warning when used |
| [`ssh-only`](#ssh-only) | off | take part only when the session arrived over SSH |
| [`mode=auto\|sudo\|console`](#modeautosudoconsole) | `auto` | forces a flow |
| [`debug`](#debug) | off | logs each check's decision |

Durations use Go's `time.ParseDuration` grammar -- `2s`, `500ms`, `1h30m`,
`1.5h` -- because that is what existing `pam.d` lines already contain. An
unparseable duration is silently the default. An unknown argument is ignored.

### `server=URL`

**Required.** The `ssoosshd` base URL. A missing scheme becomes `https://`, and
trailing slashes are trimmed.

Absent or empty is `PAM_USER_UNKNOWN`, returned before any key is generated or
any socket opened. Longer than 512 bytes is `PAM_NO_MODULE_DATA`.

### `trusted-ca-file=PATH`

**Required.** CA public keys in `authorized_keys` format, one per line, read on
every attempt so a deployment can rotate CAs without a restart. See
[the trusted CA file](/ssoossh/hosts/pam/trusted-ca/).

A key of an algorithm this build cannot verify is skipped with a warning naming
it and the line; if no usable key remains, that is `PAM_NO_MODULE_DATA`, the
same code an unreadable file produces.

### `principals-map=PATH`

Optional in the module, needed in practice. Maps a local account to the
certificate principals allowed to assume it -- see
[the principals map](/ssoossh/hosts/pam/principals-map/).

A map that is configured but fails to load logs a warning and falls back to
requiring the certificate to carry the exact account name; it does not fail the
login, so a mistyped path degrades to the stricter default rather than locking
every account out of the host. A map that loads is authoritative: an account
with no entry in it is denied.

### `skew-tolerance=DURATION`

Default `2s`. Applied symmetrically to both ends of the certificate's validity
window, to absorb clock skew between the issuing server and this host.

Prefer keeping clocks synchronised to widening this: a wider window is also a
longer life for a certificate that leaks.

### `timeout=DURATION`

Default `60s`. Bounds the whole attempt, not only the waiting half. The
server's own expiry for the request bounds it further when that is the earlier
of the two.

### `insecure-skip-verify`

Default off. Accepts the bare word, `=true` or `=false`. Disables TLS
verification of the `ssoosshd` certificate.

With this on, nothing the server says is authenticated, including the URL that
reaches the terminal; the module logs a warning on every attempt that uses it.
A value that is neither `true` nor `false` is treated as `false`.

### `ssh-only`

Default off. Accepts the bare word, `=true` or `=false`. Takes part only when
the session arrived over SSH. For any other login the module returns
`PAM_IGNORE` before generating a key or opening a socket, and the stack
continues to whatever follows it.

This is for a host whose local logins already carry a factor a remote login
cannot use: a Mac with Touch ID, a workstation with a smartcard reader. Someone
at the keyboard gets that factor, and someone who arrived over `ssh` gets the
browser flow.

A session counts as SSH when either test says so:

| Test | Survives |
| --- | --- |
| `SSH_CONNECTION`, `SSH_CLIENT` or `SSH_TTY` is set in the environment | `tmux` and `screen`, where the process tree no longer leads back to `sshd` |
| An `sshd` process, or `sshd-session` on OpenSSH 9.8 and later, is this process or one of its ancestors | a scrubbed environment, where those variables are gone |

:::caution
This chooses which factor to ask for. It is not a security boundary, and every
branch of it still authenticates. A local user who exports `SSH_TTY` gets the
browser flow instead of Touch ID, which is a worse experience rather than a
weaker check, and a remote user cannot hide `sshd` from the process table.
Do not reach for this as a way to keep someone at the console out.
:::

Independent of `mode`. See [sudo and su](/ssoossh/hosts/pam/sudo/#macos-and-touch-id)
for the macOS stack this exists for.

### `mode=auto|sudo|console`

Default `auto`, described under [Modes](#modes-and-which-flow-runs). `sudo` and
`console` force a flow.

An unrecognised value is `PAM_NO_MODULE_DATA`, never a silent fall back.
Console mode is compiled in on Linux and FreeBSD only; on macOS `mode=console`
is refused with the same code and `mode=auto` always uses the browser flow.

### `debug`

Default off. Logs the decision for each of the four checks, the URL sent to the
terminal, and the request context at `LOG_DEBUG`. The bare word, or any value
other than `false`, turns it on.

The legacy value `debug=stdout` is accepted and treated as plain `debug`:
writing to a stream that belongs to `sudo` is the one thing this module never
does.

## The four checks

Run in order on the certificate the server issues. Every one has to pass, and
every failure produces `PAM_AUTH_ERR` and logs its reason at `LOG_ERR`.

1. **CA signature** -- the certificate is cryptographically signed by a key in
   `trusted-ca-file`. Not a string comparison against that file's contents.
2. **Key binding** -- the certificate's public key is the one generated for
   this attempt. Without it, checks 1, 3 and 4 passing together would accept
   any CA-signed certificate carrying the right principal, including one issued
   to somebody else's keypair.
3. **Principal** -- the certificate's principals authorise this local account,
   through the principals map when one is loaded, and by exact name otherwise.
4. **Validity window** -- now is within `[valid_after, valid_before]` plus or
   minus `skew-tolerance`.

The per-attempt keypair is always ECDSA P-384, which every platform's crypto
supports, so only the CA side is ever constrained by
[certificate algorithms](#certificate-algorithms).

## Return values

| Value | Means |
| --- | --- |
| `PAM_SUCCESS` | The certificate passed all four checks. |
| `PAM_USER_UNKNOWN` | `pam_get_user` failed, or `server` is not configured. |
| `PAM_NO_MODULE_DATA` | The argument vector could not be read, an argument is malformed or too long, or the trusted CA file is missing or holds no usable key. |
| `PAM_AUTH_ERR` | The request resolved and the answer was no: denied, expired, the server could not issue, the certificate did not parse, or a check failed. Also a timeout. |
| `PAM_IGNORE` | The person pressed Ctrl-C at the approval prompt, or [`ssh-only`](#ssh-only) is set and the session did not arrive over SSH. The module contributes nothing and the stack continues to whatever follows it. |
| `PAM_AUTHINFO_UNAVAIL` | `ssoosshd` could not be reached, or answered with something that was not an answer. This lets a `sufficient` stack fall through, so an ssoossh outage does not become a `sudo` outage on every host. A server that answered and said no is `PAM_AUTH_ERR` instead. |
| `PAM_ABORT` | The keypair or the HTTP client could not be constructed. |

## PAM items

The module reads `PAM_USER`, which names the account, and `PAM_SERVICE`,
`PAM_TTY` and `PAM_RHOST`, which go to `ssoosshd` as part of the request
context below. `PAM_TTY` and `PAM_RHOST` also decide the flow, as described
under [Modes](#modes-and-which-flow-runs).

## Request context

Every request, in either flow, carries what this process can say about itself
and where it is, so the approver can tell a request they caused from one they
did not.

| Sent | Detail |
| --- | --- |
| Hostname | The host's own name |
| PAM service, terminal, remote host, requesting user | From the items above |
| Host process command line | Linux and FreeBSD only |
| Calling uid, pid and parent pid | Of the process the module is loaded into |
| Machine identifier | `/etc/machine-id` on Linux, the host UUID elsewhere |
| Operating system | From `/etc/os-release` and `uname` |
| Module name and version | This module's own |
| Configured `mode` | `auto`, `sudo` or `console` as set |
| The host's clock | What this host believes the time is |
| CA fingerprints | The SHA256 fingerprint of each key in `trusted-ca-file`, up to eight |

:::caution
Every one of these is self-reported by an unauthenticated caller. The module
verifies none of them before sending, and the server shows them as claims, not
facts. A field the host cannot supply is left out rather than guessed.
:::

Earlier releases sent this context on the console path only. It now goes with
both flows, so a `sudo` approval shows the same detail a console approval does.

Everything written to the terminal goes through the PAM conversation function
the application supplied; the module never opens the tty itself.

## Network

Requests go over HTTPS with **TLS 1.3 only**, verified against the operating
system's trust store; there is no option for a private CA bundle other than
installing the CA into that store. Redirects are not followed. Connecting is
bounded at ten seconds and the whole attempt by `timeout`. While waiting, the
module holds one streaming connection open for the approval and reconnects if
it drops.

The HTTP client is `libcurl`, which honours the `http_proxy`, `https_proxy` and
`no_proxy` environment variables. Inside `sudo` those are normally scrubbed
from the environment, so a host that needs a proxy for the module has to
arrange it in the calling process.

## Logging

Everything goes to `syslog(3)` under `LOG_AUTHPRIV`. Nothing is written to
stdout or stderr. `openlog(3)` and `closelog(3)` are never called: the first
mutates process-global state and the second closes a descriptor the host
process may be holding, and inside `sudo` neither is this module's to touch.
Every message carries its own `pam_ssoossh:` prefix instead.

Each attempt logs one unconditional line naming the module version, the
`ssoosshd` release it was qualified against, and the crypto and HTTP libraries
actually linked into the process:

```text
pam_ssoossh: 1.0.0 | ssoosshd: v1.0.0 | crypto: OpenSSL 1.1.1k | fips: off | http: libcurl/7.61.1 OpenSSL/1.1.1k
```

| Field | Is |
| --- | --- |
| `1.0.0` | The module version |
| `ssoosshd:` | The server release this build was tested against, not the server it is talking to. The module and the server are versioned independently |
| `crypto:` | The crypto library the module itself calls, as that library reports itself. On macOS, `Security.framework` plus the Ed25519 SPI self-test result |
| `fips:` | `on` or `off`, the host's FIPS mode. Absent on macOS, which has no such switch |
| `http:` | What libcurl reports for itself and for the TLS library it drives |

Two OpenSSLs appear because there are two: the one the module calls directly,
and the one libcurl drives for TLS. The second is historically the larger
attack surface -- X.509 chain parsing of whatever certificate `ssoosshd`
presents -- so it is named on its own.

The module links crypto rather than shipping it, so which OpenSSL is resident
in `sudo`, and whether it is in FIPS mode, is a property of the host. That line
is what makes the question answerable across a fleet by grepping syslog,
including for a host whose distribution has stopped issuing updates.

Failures log their reason at `LOG_ERR`, a successful authentication at
`LOG_INFO`, and the per-check reasoning at `LOG_DEBUG` when `debug` is set.

Text that reaches the terminal is filtered first: the approval URL is reduced
to the RFC 3986 character set, the console code to its base32 alphabet, and the
QR code to the three block characters it is drawn from. A server response that
lost characters to that filter is logged as a warning, because it is either an
`ssoosshd` that changed shape or a hostile one.

## Certificate algorithms

Which CA key types work is decided by the crypto the platform ships. Anything
unsupported fails with an error naming the algorithm, never with a vague
signature failure.

| CA key type | Linux, FreeBSD | macOS |
| --- | --- | --- |
| `ecdsa-sha2-nistp256` / `384` / `521` | yes | yes |
| `rsa-sha2-256`, `rsa-sha2-512` | yes | yes |
| `ssh-ed25519` | yes | yes (macOS 14+) |
| `ssh-rsa` (SHA-1) | refused | refused |

`ssh-rsa` names RSA with SHA-1, which OpenSSH has disabled by default since
8.8. It is refused by policy on every platform.

On macOS, Ed25519 is verified through a Security.framework interface Apple
exports but does not document. The module resolves it at runtime, self-tests it
before use, and names the outcome in the version line it logs on every
authentication: `crypto: Security.framework (Ed25519 SPI ok)` when it works,
and `(no Ed25519 SPI)` or `(Ed25519 SPI FAILED self-test)` when it does not --
in which case an `ssh-ed25519` CA is skipped with a warning naming its type,
and every other CA type still works.

## FIPS mode

The module makes no cryptographic choices a FIPS policy could object to, and
defers the ones it cannot make to the host. The per-attempt keypair is ECDSA
P-384 on every platform. Which CA algorithms verify is whatever the host's
crypto library allows in its current configuration, found out by asking it
rather than by table:

- RSA and ECDSA certificates go through the same OpenSSL calls with FIPS on or
  off. Where the FIPS module refuses one -- an RSA CA below the approved key
  size, say -- the check fails and the log line carries OpenSSL's own reason,
  not a claim that the certificate was malformed.
- Ed25519 is the algorithm a FIPS configuration commonly lacks. On first use
  the module has the host's OpenSSL verify a known RFC 8032 vector; if that is
  refused, an `ssh-ed25519` CA is skipped with a warning naming FIPS, every
  other CA type keeps working, and a certificate signed by such a CA is refused
  as unsupported rather than as a bad signature. RHEL 8's FIPS module has no
  EdDSA; RHEL 9's OpenSSL 3.5 does, and there Ed25519 works in FIPS mode.
- TLS to the server is `libcurl` over the same OpenSSL, under the same policy.

FIPS mode is detected from the kernel's `/proc/sys/crypto/fips_enabled` where
there is one, and from the library's own flag -- the default property query on
OpenSSL 3, the mode switch on 1.1.1 -- so a host that forces it through OpenSSL
configuration alone is also reported. The state appears in the version line of
every authentication as `fips: on` or `fips: off`. On macOS there is no such
switch, since Apple's corecrypto is validated as shipped, and the line carries
no field.

## Services

Which `pam.d` services to put the module in is an operator's decision. The
shape is always the same: one `auth sufficient` line above the service's
existing password authentication.

| Service | Notes |
| --- | --- |
| `sudo` | The original use. The browser flow, since a `sudo` session has a terminal emulator or SSH session behind it. See [sudo and su](/ssoossh/hosts/pam/sudo/) |
| `su` | The same, after the `pam_rootok.so` line that lets root switch without authenticating |
| `sshd` | Works through keyboard-interactive authentication, which is how `sshd` runs PAM and relays the module's message to the client. Needs `UsePAM yes` and `KbdInteractiveAuthentication yes`. Always the browser flow, since `PAM_RHOST` is set. Public-key logins are unaffected unless `AuthenticationMethods` requires both. See [sshd keyboard-interactive](/ssoossh/hosts/pam/sshd/) |
| `login`, `getty` | A console login, so `mode=auto` runs the console flow. Consider gating the line with `pam_succeed_if.so` so that root, or accounts outside a group, never see it, and leave `pam_securetty.so` where it is. See [console login](/ssoossh/hosts/pam/console/) |

:::danger
Do not add the module to a shared include such as `common-auth`,
`system-auth` or `base-auth`: screen lockers, display managers and `polkit`
read those too, and an approval prompt that cannot be seen is a lockout.

Test from a second, already-open root session, never from the one whose service
is being edited, and keep a copy of the file being changed.
:::

## Principals

A certificate's principals are decided by `ssoosshd`, and for a PAM request
they carry the **approver's** identity -- the username of the person who
approved, and any other accounts the server has recorded them as holding --
never the local account name that was asked for.

A host whose local accounts are not spelled exactly as the identity provider
spells its users therefore needs a
[principals map](/ssoossh/hosts/pam/principals-map/) for anyone to log in at
all.

## Files

| Path | Is |
| --- | --- |
| `/etc/pam.d/*` | The service stanzas that load this module |
| `/lib/*/security/pam_ssoossh.so` | The module, wherever libpam looks on this platform: `/usr/lib/<triplet>/security` on Debian and Ubuntu, `/usr/lib64/security` on RHEL and its rebuilds, `/lib/security` on Alpine |
| `/usr/share/doc/pam-ssoossh/examples/` | Shipped by the packages: a commented `pam.d` fragment for each service under `pam.d/`, and a `principals.yaml` to start from |
| `/etc/ssoossh/ca.pub` | The trusted CA file, at the path the examples use |
| `/etc/ssoossh/principals.yaml` | The principals map, at the path the examples use |

Nothing in the module fixes the last two paths; both are named on the `pam.d`
line.

## Compatibility

Behaviour worth knowing before writing a `pam.d` line against this module:

- `PAM_IGNORE` is returned on Ctrl-C, so the stack continues to whatever
  follows rather than counting the interruption as a failed authentication.
- `ssh-rsa` (SHA-1) CA keys are refused by policy. OpenSSH has disabled that
  signature algorithm by default since 8.8; check what your CA key is with
  `ssh-keygen -l -f /etc/ssoossh/ca.pub`.
- Log lines carry their own `pam_ssoossh:` prefix rather than calling
  `openlog`, so syslog attributes them to the host process.
- Text sent to the terminal is filtered first, as described under
  [Logging](#logging).

## See also

`pam.conf(5)`, `pam.d(5)`, `pam(8)`, `sudo(8)`, `sshd_config(5)`,
`syslog(3)`, `ssh-keygen(1)`. On a host with the package installed:
`man 8 pam_ssoossh`, `man 5 pam_ssoossh-ca`, `man 5 pam_ssoossh-principals`.
