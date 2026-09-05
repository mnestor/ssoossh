---
title: Client configuration
description: Where ssoossh.yaml is found, what it can set, and which source wins when two disagree.
eyebrow: User guide
sidebar:
  order: 2
---

The client reads a YAML file called `ssoossh.yaml`. Every setting in it has a
working default except one, so a usable file can be a single line. This page
explains where that file is looked for, how several of them combine, and which
one wins. Every key named here links to
[the reference page](/ssoossh/reference/client-config/), which has the type,
the default, and the full description.

## The one setting with no default

```yaml
# ~/.config/ssoossh.yaml
server: "https://ssh.example.com"
```

[`server`](/ssoossh/reference/client-config/#server) is the base URL of the
ssoosshd server. Without it, every command that talks to the server fails.
`--server` overrides it for one invocation, which is how you test a new address
before writing it down.

## Where the file is looked for

Up to four files are consulted, each merged on top of the last. A missing file
at any of them is normal, not an error.

| Order | Location on Linux and macOS | Location on Windows |
| --- | --- | --- |
| 1 | built-in defaults (compiled into the binary) | same |
| 2 | `/etc/ssoossh/ssoossh.yaml` | `%ProgramData%\ssoossh\ssoossh.yaml` |
| 3 | `~/.config/ssoossh.yaml` | `%AppData%\ssoossh\ssoossh.yaml` (the roaming profile) |
| 4 | `./ssoossh.yaml` (working directory) | `.\ssoossh.yaml` |
| 5 | the file named by `--config` | same |

The machine-wide file at position 2 is special in one way: it is the only
location the [`enforce`](/ssoossh/reference/client-config/#enforce) key is read
from. The per-user file is skipped entirely when no home directory can be
determined, which is the right answer for a machine account rather than an
error.

The `.deb` and `.rpm` packages install an annotated copy of the built-in
defaults to `/etc/ssoossh/ssoossh.yaml`, so a fresh install starts from a file
that documents itself. The commented-out keys in it are the ones with no
built-in value; uncommenting one is a real change even when you write down what
it already does.

A file named with `--config` is treated more strictly than the search paths: if
it is missing or will not parse, the command fails instead of continuing with
every setting in it silently absent.

## Precedence

Lowest to highest. Each source overrides any earlier one that sets the same
key; a key a source does not mention is left to whatever set it below.

1. Built-in defaults
2. The machine-wide file (`/etc/ssoossh/ssoossh.yaml`)
3. The per-user file (`~/.config/ssoossh.yaml`)
4. The local file (`./ssoossh.yaml`)
5. `--config <file>`
6. Command-line flags
7. The [`enforce`](/ssoossh/reference/client-config/#enforce) file named by the
   machine-wide file
8. Platform-native policy: Windows Group Policy registry values, macOS managed
   preferences

Nothing is guessed about where a value came from: `--debug` prints the whole
chain in this order, with what came of each entry, and marks the two
administrator-locked tiers. That is the fastest way to answer "why is it using
that server". See [Diagnostics](/ssoossh/guides/diagnostics/).

## The `enforce` file

If the machine-wide file sets `enforce`, naming another YAML file, that file is
merged after the per-user file, the local file, and `--config` -- so it wins
over every location a non-administrator can write to. A relative `enforce` path
resolves inside the system directory, so it cannot be redirected to a file the
user controls.

```yaml
# /etc/ssoossh/ssoossh.yaml -- the machine-wide file
enforce: ssoossh-locked.yaml
```

```yaml
# /etc/ssoossh/ssoossh-locked.yaml
server: "https://ssh.example.com"
use_agent: true
```

No file other than the machine-wide one may set `enforce`; setting it anywhere
else has no special effect. Unlike the optional files above, a missing or
malformed `enforce` target is a hard startup error -- silently dropping every
locked setting is exactly what the mechanism exists to prevent.

:::note
Enforcement is a guardrail, not a security boundary. The client runs as the
user, who can always supply their own binary. The one setting actually enforced
beyond client cooperation is `cert_options.*.valid_duration` on the server,
which decides how long an issued certificate is usable. Windows and macOS can
lock the same settings through Group Policy and managed preferences instead;
[Client settings enforcement](/ssoossh/hosts/client-enforcement/) has the full
mapping.
:::

### The flag caveat

The settings that also have command-line flags -- `--server`, `--key-type`,
`--key-size`, and the `--no-*` extension flags -- are bound into the
configuration as flags rather than merged as files. If the user actually passes
one, it takes precedence over every config-file tier, including `enforce` and
platform policy. This is the same "guardrail, not a boundary" situation as
running your own binary.

The one thing a flag cannot do is re-add a certificate extension that platform
policy forbids: that subtraction happens after flags and config are resolved.

## What you can set

The keys fall into four groups.

**Which server, and how to trust it.**
[`server`](/ssoossh/reference/client-config/#server) is the address.
[`capubkey`](/ssoossh/reference/client-config/#capubkey) pins the server's CA
public key so the client does not fetch it at startup.
[`insecure_skip_verify`](/ssoossh/reference/client-config/#insecure_skip_verify)
turns off TLS verification, which is for development against a self-signed
certificate and defeats the point of pinning `capubkey`.

**Where keys and certificates live.**
[`use_agent`](/ssoossh/reference/client-config/#use_agent) is true by default
and is the recommended mode; it is also the only mode
[`ssh proxycommand`](/ssoossh/guides/ssh-config/) supports. Turning it off
means the agent is not consulted at all, not merely deprioritized.
[`fallback_file_agent`](/ssoossh/reference/client-config/#fallback_file_agent)
decides what happens when an agent was wanted and none could be reached: fall
back to key files, or fail closed.
[`key_filename`](/ssoossh/reference/client-config/#key_filename) names the
files used in either of those cases.

```yaml
use_agent: true
fallback_file_agent: true
key_filename: id_ssoossh
```

**What key to generate, and what to ask for.**
[`sshkey.type`](/ssoossh/reference/client-config/#sshkeytype) and
[`sshkey.size`](/ssoossh/reference/client-config/#sshkeysize) select the
algorithm for the fresh keypair generated on every certificate request.
[`fips`](/ssoossh/reference/client-config/#fips) steers that choice toward
algorithms FIPS-mode SSH implementations accept.
[`certificate_extensions`](/ssoossh/reference/client-config/#certificate_extensions)
opts out of individual extensions the client would otherwise request.

**Comfort.**
[`try_open_browser`](/ssoossh/reference/client-config/#try_open_browser) makes
a best-effort attempt to launch the approval URL. The URL is always printed
first, and a launch failure never fails the login, so a headless machine loses
nothing by leaving it off.

## A complete example

```yaml
server: "https://ssh.example.com"

insecure_skip_verify: false

use_agent: true
fallback_file_agent: true
key_filename: id_ssoossh
try_open_browser: true

sshkey:
  type: ecdsa
  size: 384

certificate_extensions:
  no_x11_forwarding: true
```

Every key, with its type and default:
[ssoossh.yaml reference](/ssoossh/reference/client-config/).
