---
title: The ssoossh client
description: Install the client, wire it into ssh_config, and log in.
eyebrow: Guides
sidebar:
  order: 1
---

The client is never run on its own. `ssh` invokes it, it makes sure a valid
certificate is loaded, and then the connection proceeds as usual.

## Install

**Debian / Ubuntu**

```sh
sudo dpkg -i ssoossh-client_*.deb
```

**RHEL / Fedora**

```sh
sudo rpm -i ssoossh-client_*.rpm
```

**Windows** -- download the `.zip` from the release and extract `ssoossh.exe`
somewhere on `PATH`.

**macOS** -- download the `.zip` from the release. The binary inside is signed
and notarized, so Gatekeeper does not block it. Extract and place `ssoossh` on
`PATH`.

## Point it at your server

One setting has no default:

```yaml
# ~/.config/ssoossh.yaml
server: "https://ssh.example.com"
```

Check that it resolves before trying a real login. `--debug` reports the merge
chain and the resolved settings, and works on any command:

```sh
ssoossh --debug ca
```

## Wire up `ssh_config`

There are two modes, and the difference that matters is what happens after a
certificate is issued.

| | `Match exec` + `ssh login` | `ProxyCommand` |
| --- | --- | --- |
| Client after issuance | exits | stays running, relays the connection |
| ssh-agent | optional; key files on disk also work | **required** |

`ssh` reads `IdentityFile` and `CertificateFile` once at startup. That is why
`ProxyCommand` needs an agent: a certificate refreshed on disk after that point
is never re-read.

### `Match exec` (recommended)

Runs before `ssh` connects. An already-loaded, still-valid certificate is
reused, so this costs no browser round trip until it expires. A non-zero exit
blocks the connection.

```ssh-config
Match host bastion.example.com exec "ssoossh ssh login"
    User youruser
```

### `ProxyCommand`

Ensures a valid certificate, then hands off to whatever relay command you give
it. Useful when reaching the target also requires an HTTP or SOCKS proxy:

```ssh-config
Host jump.example.com
    ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p
```

`ssoossh ssh config` prints both recipes and nothing else. It is offline -- it
contacts no server and needs none configured -- because these are the lines you
go looking for when the connection is the broken thing.

## Commands

```
ssoossh ssh login | logout | proxycommand | inspect | config
ssoossh host principals | mapping (list | add | remove)
ssoossh service enroll | retrieve
ssoossh ca
ssoossh version
```

`ssoossh ca` prints the CA public key, which is what target hosts need to trust
the certificates your server signs:

```sh
ssoossh --server https://ssh.example.com ca > /etc/ssh/ssoossh_ca.pub
```

Then on each target host:

```sshd-config
# /etc/ssh/sshd_config
TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub
```

Fetching the key from the running server confirms the deployed key rather than
trusting a local copy blindly.

## Configuration

Everything except `server` has a working default. The two worth knowing:

```yaml
server: "https://ssh.example.com"

# Store keys in ssh-agent (the default and recommended mode). If the agent
# is unreachable and fallback_file_agent is true, the client falls back to
# key files automatically; with fallback disabled, the login fails instead.
use_agent: true
fallback_file_agent: true

# The generated keypair's algorithm.
sshkey:
  type: ed25519
```

With `use_agent: false` the client stores key files on disk, and only the
`Match exec` mode works.

### Where the config comes from

Lowest to highest. Each source is overridden by any later one setting the same
key:

1. Built-in defaults
2. User config (`~/.config/ssoossh.yaml`, `%AppData%\ssoossh\ssoossh.yaml`)
3. Local config (`./ssoossh.yaml`, or `--config`)
4. CLI flags (`--server`, `--key-type`, `--key-size`)
5. The `enforce` file named by the machine-wide config
6. Platform policy: Windows Group Policy registry keys, macOS managed preferences

:::note
Enforcement is a guardrail, not a security boundary -- the client runs as the
user. The one setting enforced beyond client cooperation is
`cert_options.*.valid_duration`, which the server applies when it signs.
:::

## Diagnostics

Both flags write to stderr, and both have an environment equivalent for
invocations whose command line belongs to `ssh` or cron.

| Flag | Environment | Prints |
| --- | --- | --- |
| `-v`, `-vv`, `-vvv` | `SSOOSSH_VERBOSE=1..3` | steps, then requests and files, then bodies |
| `--debug` | `SSOOSSH_DEBUG=1` | the config merge chain, resolved settings, and key file paths |

`--debug` implies `-v`. Run it on the command you are actually diagnosing: an
offline command never builds key storage or resolves the CA, so those stay
unreported. `-v` output is what a bug report should include.
