---
title: The ssoossh client
description: Every ssoossh subcommand, what it does, and the flags it takes.
sidebar:
  order: 0
---

The client is what `ssh` invokes on your behalf. It generates a keypair, hands
the public key to the server, waits for your approval in a browser, and loads
the signed certificate into your ssh-agent -- or writes key files when no agent
is available. Private keys never leave the machine. This page is the command
reference: what each subcommand does and what it accepts.

## Install

Release packages are on the
[releases page](https://github.com/mnestor/ssoossh/releases).

| Platform | Package | Where it lands |
| --- | --- | --- |
| Debian / Ubuntu | `ssoossh-client_*.deb` | `/usr/local/bin/ssoossh`, sample config at `/etc/ssoossh/ssoossh.yaml` |
| RHEL / Fedora | `ssoossh-client_*.rpm` | same |
| Windows | `ssoossh-client_*.zip` | extract `ssoossh.exe` onto `PATH` |
| macOS | `ssoossh-client_*_darwin_<arch>.pkg` | `/usr/local/bin/ssoossh`, man pages under `/usr/local/share/man`, the annotated defaults at `/usr/local/share/ssoossh/ssoossh.yaml` |
| macOS | `ssoossh-client_*.zip` | the same files, unpacked by hand -- extract `ssoossh` onto `PATH` |

There is one macOS package per architecture, `darwin_arm64` for Apple
silicon and `darwin_amd64` for Intel; the installer refuses the wrong one
rather than leaving you with a binary that will not run. Both the binary and
the package are Developer ID signed and notarized, so neither prompts
Gatekeeper. Check a download before opening it:

```bash
spctl --assess --type install -v ssoossh-client_1.2.3_darwin_arm64.pkg
```

The package writes nothing under `/etc`. It installs the annotated defaults
to `/usr/local/share/ssoossh/ssoossh.yaml` as a file to copy from -- the
client reads `/etc/ssoossh/ssoossh.yaml`, `~/.config/ssoossh.yaml` and
`./ssoossh.yaml`, in that order, and needs none of them, since the same
defaults are compiled in. To remove it:

```bash
sudo rm -rf /usr/local/bin/ssoossh /usr/local/share/ssoossh \
    /usr/local/share/doc/ssoossh-client \
    /usr/local/share/man/man1/ssoossh*.1 \
    /usr/local/share/man/man5/ssoossh.yaml.5
sudo pkgutil --forget org.mikenestor.ssoossh-client
```

## Global flags

These are persistent flags: every subcommand accepts them.

| Flag | Default | What it does |
| --- | --- | --- |
| `-c`, `--config <file>` | none | Path to the ssoossh config file. Merged after every search-path location. A file named here that is missing or unparseable is a startup error, unlike an absent search-path file. |
| `--server <url>` | none | Server address including scheme, for example `https://example.com`. `https` is assumed if the scheme is omitted. Overrides `server` in the config file. |
| `-v`, `-vv`, `-vvv` | off | Trace what the command is doing, to stderr. `-v` steps, `-vv` requests and files, `-vvv` bodies. |
| `--debug` | off | Print the resolved configuration, its sources, and key storage to stderr. Hidden from `--help` but supported; implies `-v`. |
| `-h`, `--help` | | Help for the command. |

`-v` and `--debug` both have environment equivalents for invocations whose
command line belongs to `ssh` or cron. See
[Diagnostics](/ssoossh/guides/diagnostics/).

## The commands

| Command | Talks to the server | What it does |
| --- | --- | --- |
| [`ssh login`](#ssoossh-ssh-login) | yes | Authenticate via OIDC and load a signed certificate |
| [`ssh logout`](#ssoossh-ssh-logout) | yes | Remove ssoossh-managed keys and certificates |
| [`ssh config`](#ssoossh-ssh-config) | no | Print the `ssh_config` recipes |
| [`ssh proxycommand`](#ssoossh-ssh-proxycommand) | yes | Ensure a certificate, then relay stdio to the target |
| [`ssh inspect`](#ssoossh-ssh-inspect) | yes | Print what the held certificates grant |
| [`ca`](#ssoossh-ca) | yes | Print the server's CA public key |
| [`host principals`](#ssoossh-host-principals) | no | Answer sshd's `AuthorizedPrincipalsCommand` |
| [`host mapping`](#ssoossh-host-mapping) | no | Manage the local principal mapping file |
| [`service enroll`](#ssoossh-service-enroll) | yes | Enroll a keypair for unattended issuance |
| [`service retrieve`](#ssoossh-service-retrieve) | yes | Redeem an enrollment code for a certificate |
| [`version`](#ssoossh-version) | no | Print version, commit, and build info |

The four offline commands (`ssh config`, `host principals`, `host mapping`,
`version`) make no network call at all. They still load configuration, because
even a command that never talks to the server has to know where its own local
files live.

## `ssoossh ssh login`

Generates a fresh keypair, sends the public key to the server, opens the
browser for OIDC approval, and waits over SSE for the signed certificate. This
is what an `ssh_config` `Match exec` line runs.

A certificate that is already loaded and still valid is reused rather than
replaced, so one approval covers every connection until it expires.

| Flag | Default | What it does |
| --- | --- | --- |
| `--force` | off | Request a new certificate even if a valid one is already loaded |
| `--key-type <type>` | `ecdsa` | Key algorithm to generate: `ed25519`, `ecdsa`, or `rsa` |
| `--key-size <n>` | `384` for ecdsa | Bits for rsa, curve for ecdsa; ignored for ed25519 |
| `--no-pty` | off | Do not request the `permit-pty` extension |
| `--no-agent-forwarding` | off | Do not request `permit-agent-forwarding` |
| `--no-port-forwarding` | off | Do not request `permit-port-forwarding` |
| `--no-x11-forwarding` | off | Do not request `permit-X11-forwarding` |
| `--no-user-rc` | off | Do not request `permit-user-rc` |

`--key-type` and `--key-size` override
[`sshkey.type`](/ssoossh/reference/client-config/#sshkeytype) and
[`sshkey.size`](/ssoossh/reference/client-config/#sshkeysize); the `--no-*`
flags override the matching
[`certificate_extensions`](/ssoossh/reference/client-config/#certificate_extensions)
keys.

```bash
# what ssh runs for you
ssoossh ssh login

# replace the loaded certificate with a fresh one
ssoossh ssh login --force

# a session that needs no forwarding of any kind
ssoossh ssh login --no-agent-forwarding --no-port-forwarding --no-x11-forwarding
```

By default the client asks for the full interactive extension set:
`permit-pty`, `permit-agent-forwarding`, `permit-port-forwarding`,
`permit-X11-forwarding`, and `permit-user-rc`. Asking for the full set is
always safe -- the server narrows the request against its own configuration
before signing, and the approval page shows what survived. Asking for nothing
is not useful: narrowing is an intersection, so an empty request yields a
certificate that cannot open an interactive session.

Before requesting anything, `ssh login` verifies that the resolved key storage
will actually accept and release a key, using a throwaway keypair. That check
exists so a storage failure surfaces before you approve a certificate rather
than after.

## `ssoossh ssh logout`

Removes the certificates ssoossh put in your ssh-agent -- those signed by the
configured CA -- and nothing else. Your own keys are left alone. When key files
are used instead of an agent, the files ssoossh wrote are deleted.

No flags beyond the global ones.

```bash
ssoossh ssh logout
```

## `ssoossh ssh config`

Prints the `ssh_config` recipes that make `ssh` invoke ssoossh, with the rules
that decide which one you want. It contacts nothing and reads nothing but that
text, so it answers before a server is configured and keeps answering after one
stops responding -- which is exactly when you go looking for it.

No flags beyond the global ones.

```bash
ssoossh ssh config
```

It does not report resolved settings. `--debug` is the one place those are
printed, so there is no second version of the truth to keep in step. See
[ssh_config integration](/ssoossh/guides/ssh-config/) for the long form.

## `ssoossh ssh proxycommand`

For use as `ssh_config`'s `ProxyCommand`. It ensures a valid certificate, then
relays stdio to the target host. The arguments after it mirror exactly what you
would have written without ssoossh.

No flags beyond the global ones; everything after the command name is the relay
command and its arguments.

```ssh-config
# before
ProxyCommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p
# after
ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p
```

:::caution
Key files do not work here. `ssh` reads `IdentityFile` and `CertificateFile`
once at startup and never sees a certificate written after that, so this mode
requires an ssh-agent.
:::

## `ssoossh ssh inspect`

Shows what each certificate ssoossh is holding actually grants, rather than the
certificate blob, which tells a human nothing.

No flags beyond the global ones.

```bash
ssoossh ssh inspect
```

The report opens with `Storage`, naming where the certificates are kept: an
ssh-agent holds them in a process, and a file backend adds a `Key files` list of
the paths on disk (private key, public key, certificate) so you know which files
the certificates below came out of.

Each certificate then prints as `Principals`, `Key ID`, `Type`, `Expires`,
`Serial`, `Extensions`, and `Critical options`. With none loaded it says so,
naming the storage backend it looked in.

## `ssoossh ca`

Retrieves the CA public key of the configured server. This is what target hosts
must trust.

No flags beyond the global ones.

```bash
ssoossh --server https://ssh.example.com ca > /etc/ssh/ssoossh_ca.pub
```

Then on each target host:

```ini
# /etc/ssh/sshd_config
TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub
```

Fetching the key from the running server confirms the deployed key rather than
trusting a local copy blindly. [Trusting the CA in
sshd](/ssoossh/hosts/sshd-trust/) covers the host side.

## `ssoossh host`

Configures the principal mapping used when `sshd` asks for authorized
principals via `AuthorizedPrincipalsCommand`. Nothing under `host` talks to the
server.

| Flag | Default | What it does |
| --- | --- | --- |
| `--file <path>` | `/etc/ssoossh/principals.yaml` | The local principal mapping file: YAML listing, per local account, the principals allowed to assume it |

`--file` is inherited by every `host` subcommand.

### `ssoossh host principals`

Implements `AuthorizedPrincipalsCommand`. It is called on every login attempt
and never touches the network -- it answers only from the local mapping file.
It expects one argument, the local username to look up, and prints one
principal per line.

The looked-up name is always among them. That is what `sshd` accepts unaided
-- with no `AuthorizedPrincipalsCommand` configured it admits a certificate
carrying the target account name -- so installing this command does not take
it away from an account whose mapping does not restate it. The mapping file
adds principals; it cannot remove the account's own name.

| Situation | Behavior |
| --- | --- |
| Account found | Prints its principals in file order, then the account name if the file did not already list it, exit 0 |
| Unknown account, or missing file | Prints the account name alone, exit 0 |
| Unreadable or malformed file | Non-zero exit, no output at all |

It needs no privilege beyond read access to the mapping file, so run it as a
dedicated unprivileged account rather than as root:

```ini
# /etc/ssh/sshd_config
AuthorizedPrincipalsCommand /usr/local/bin/ssoossh host principals %u
AuthorizedPrincipalsCommandUser ssoossh-principals
```

See [trusting the CA in sshd](/ssoossh/hosts/sshd-trust/#answering-with-a-command)
for creating that account and the file permissions it needs.

### `ssoossh host mapping`

Manages that same file.

| Command | Arguments | What it does |
| --- | --- | --- |
| `host mapping list` | none | Print the current mapping |
| `host mapping add` | `<account> <principal>` | Add a principal to the account, deduplicating; the principal's syntax is validated before anything is written |
| `host mapping remove` | `<account> <principal>` | Remove that principal from the account (a no-op if it is not present) |
| `host mapping remove` | `<account>` | Remove the entire account mapping |

```bash
sudo ssoossh host mapping add deploy alice@example.com
sudo ssoossh host mapping list
sudo ssoossh host mapping remove deploy alice@example.com
```

:::note
There are no host certificates. `host mapping` and `host principals` are local
tooling for `AuthorizedPrincipalsCommand`; they have no server side.
:::

## `ssoossh service enroll`

Enrolls a keypair, bound by the server to an enrollment code, for later
unattended certificate retrieval. The keypair is either generated here or
already present on disk.

| Flag | Default | What it does |
| --- | --- | --- |
| `--key <path>` | none, required | Keypair path, relative or absolute. Generates both files if neither `<path>` nor `<path>.pub` exists; enrolls the existing `<path>.pub` otherwise |
| `--retrieve` | off | Immediately redeem the code once and write the certificate to `<path>-cert.pub` |

```bash
ssoossh service enroll --key /etc/backup/id --retrieve
```

## `ssoossh service retrieve`

Redeems an enrollment code, writing the certificate to `<path>-cert.pub` and
checking local disk first to avoid unnecessary server calls.

| Flag | Default | What it does |
| --- | --- | --- |
| `--code <code>` | none, required | The enrollment code to redeem |
| `--key <path>` | none, required | Keypair path; the certificate lands at `<path>-cert.pub` |
| `--grace <duration>` | `1m` | How much validity a certificate on disk must still have before it counts as fresh, for example `30s`, `5m`, `1h` |
| `--force` | off | Retrieve a new certificate even if a valid one exists on disk |

```bash
ssoossh service retrieve --code K7M4QP2X --key /etc/backup/id
```

Both service commands are covered in full, with worked examples, on
[Service accounts](/ssoossh/guides/service-accounts/).

## `ssoossh version`

Prints version, commit, and build info. Offline: it makes no network call.

```bash
ssoossh version
```

## Where to go next

- [ssh_config integration](/ssoossh/guides/ssh-config/) -- the two ways `ssh`
  can invoke this client.
- [Client configuration](/ssoossh/guides/client-config/) -- the `ssoossh.yaml`
  file and where each setting comes from.
- [ssoossh.yaml reference](/ssoossh/reference/client-config/) -- every key,
  type, and default.
- [Diagnostics](/ssoossh/guides/diagnostics/) -- `-v`, `--debug`, and what to
  attach to a bug report.
