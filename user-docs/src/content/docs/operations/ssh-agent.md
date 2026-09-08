---
title: The CA key in an ssh-agent
description: Holding the CA private key in an ssh-agent so it never enters ssoosshd's memory, including HSM-backed keys without a PKCS#11 module in the server.
sidebar:
  order: 15
---

`ssoosshd` can take its CA private key from a running `ssh-agent` instead of
reading it from configuration or opening a PKCS#11 token itself.

This is the only key source where the private key is neither in the server's
memory nor reachable from it. The agent protocol has no export operation, so
a compromise of `ssoosshd` is a signing oracle rather than a key disclosure.

## Why this and not `hsm`

An agent can hold a PKCS#11 token, so this is usually the better way to
reach an HSM:

| Property | `ssh_key_agent` | `hsm` |
| --- | --- | --- |
| Private key in `ssoosshd`'s memory | no | no |
| PKCS#11 module loaded into `ssoosshd` | no | yes |
| Build required | the default, cgo-free build | an `hsm`-tagged build |
| Ed25519 CA key | yes | **no** |
| Recovers from the key source restarting | yes | no, needs a restart |

The default `ssoosshd` build has no PKCS#11 support at all. It is statically
linked, which is what keeps its container image small and removes the
glibc/musl distinction entirely. Configuring `hsm` in that build fails at
startup with a message pointing here.

:::note[What an agent does not give you]
The agent signs whatever blob it is handed. A compromised `ssoosshd` can
still issue any certificate the CA could; what it cannot do is walk away
with the key. For least authority, that comes from
[split mode](/ssoossh/operations/startup-modes/), where the web tier holds
no key material at all. The two compose: run the signer in split mode, with
its key in an agent.
:::

## A plain key in an agent

```bash
ssh-agent -a /run/ssoossh/agent.sock
SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add /etc/ssoossh/ca-key
SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -l
```

The last command prints the fingerprint to configure:

```text
384 SHA256:WdJsHl3uxM2S7QwPQKBXaHAW8bKNToZMbzrcGq4DZvA ssoossh-ca (ECDSA)
```

```yaml
ssh_key_agent:
  socket: /run/ssoossh/agent.sock
  key_fingerprint: "SHA256:WdJsHl3uxM2S7QwPQKBXaHAW8bKNToZMbzrcGq4DZvA"
```

`key_fingerprint` may be omitted when the agent holds exactly one key. With
more than one it is required: `ssoosshd` refuses to pick rather than let the
CA depend on the order keys were added.

`socket` falls back to `SSH_AUTH_SOCK` when unset. Set it explicitly for a
service, where inheriting whatever socket the environment happened to carry
is not a decision anyone made.

## An HSM behind the agent

`ssh-add -s` loads a PKCS#11 module into the agent. The key stays on the
token; the agent, not `ssoosshd`, loads the vendor library.

```bash
SSH_AUTH_SOCK=/run/ssoossh/agent.sock \
  ssh-add -s /usr/lib/softhsm/libsofthsm2.so
Enter passphrase for PKCS#11:        # the token's user PIN
Card added: /usr/lib/softhsm/libsofthsm2.so
```

```bash
SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -l
384 SHA256:ceRY5xb3oIywF4Pb5QRUP3o7ZJQuUio9lqv2EzHwnlA ssoossh-ca (ECDSA)
```

Configuration is identical to the plain-key case: the module path never
appears in `ssoosshd.yaml`, because `ssoosshd` never loads it.

This works the same for a CloudHSM, Luna or YubiHSM module. It also contains
them: a memory-safety bug in a vendor PKCS#11 library corrupts the agent's
address space, not the process running the certificate pipeline.

### Unattended startup

`ssh-add -s` prompts for the PIN on a terminal. For a service, supply it
through `SSH_ASKPASS`:

```bash
cat > /etc/ssoossh/hsm-askpass <<'SH'
#!/bin/sh
cat /etc/ssoossh/hsm-pin
SH
chmod 700 /etc/ssoossh/hsm-askpass

SSH_ASKPASS=/etc/ssoossh/hsm-askpass SSH_ASKPASS_REQUIRE=force DISPLAY=:0 \
  SSH_AUTH_SOCK=/run/ssoossh/agent.sock \
  ssh-add -s /usr/lib/softhsm/libsofthsm2.so
```

:::caution
That script reads a PIN file, so it is exactly as sensitive as the PIN. The
same warning as everywhere else applies: it buys nothing if it sits beside
the token store with the same ownership. See
[the security model](/ssoossh/concepts/security-model/#what-a-passphrase-changes-and-what-it-does-not).
:::

The agent must be loaded before `ssoosshd` starts, and reloaded if the agent
restarts. `ssoosshd` reconnects to a restarted agent on its own, but it
cannot re-add a key nobody added.

## Loading the key without writing it to disk

The recipe above runs `ssh-add /etc/ssoossh/ca-key`, which means the CA key
is a file on the server. If the point of using an agent is that the key
never lands on that disk at all, the loading step must not be the thing
that puts it there.

`ssh-add -` reads the key from standard input:

```bash
pass show ssoossh/ca-key |
  sudo -u ssoossh SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -
```

```text
Identity added: (stdin) (ssoossh-ca)
```

Any command that prints the key works in place of `pass show`: a password
manager, `op read`, a `vault kv get` field, `gpg --decrypt`. What matters is
that the key travels through a pipe and never appears as an argument, so it
reaches neither the shell's history file nor anyone's `ps` output.

The same thing from a workstation, without an interactive login on the CA
host:

```bash
pass show ssoossh/ca-key |
  ssh ca-host 'sudo -u ssoossh SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -'
```

With no secret store to pipe from, run `ssh-add -` and paste the key, then
press Ctrl-D. Be aware of what that costs: the terminal echoes the key as it
arrives, so the private key ends up in scrollback, and in any session
recording or `script` transcript that is running. Clear the scrollback
afterwards, and prefer a pipe wherever there is something to pipe from.

:::caution[Three ways to spill it anyway]

- `ssh-add /path/to/key` puts the key on the disk, which is the thing this
  whole arrangement exists to avoid.
- `echo "$CAKEY" | ssh-add -` leaks through the assignment, not the pipe.
  Whatever set `$CAKEY` is in the shell's history file, and if it was
  exported, the key is readable in `/proc/<pid>/environ` for every process
  the operator runs. Relying on `HISTCONTROL=ignorespace` to suppress the
  first half is one forgotten space away from failing.
- `ssh-add - <<< "$CAKEY"` depends on the shell, invisibly. Bash 5.2 backs a
  here-string with a pipe; zsh 5.9 writes it to a temporary file under
  `/tmp` and unlinks it, so on an operator's zsh the key does reach the
  disk, in an unlinked file whose contents survive in free blocks.

:::

## Running it under systemd

`ssoosshd` resolves the CA key while it starts, so an agent holding no key
is a boot failure, not a degraded mode:

```text
ssoosshd: failed to initialize certificate pipeline: failed to load CA signing key:
ssh-agent holds no keys: load the CA key with ssh-add, or ssh-add -s <pkcs11 module> for a token
```

That rules out starting the server at boot. It is a deliberate trade rather
than a limitation: a key that exists only in an agent's memory does not
survive a reboot, so a reboot is supposed to need a human. Until one loads
the key, no certificates are issued.

Two files carry the arrangement:

```bash
cp deploy/ssoossh-agent.service /etc/systemd/system/ssoossh-agent.service
install -Dm644 deploy/ssoosshd.service.d/ssh-agent.conf \
  /etc/systemd/system/ssoosshd.service.d/ssh-agent.conf
systemctl daemon-reload
systemctl disable ssoosshd
```

`systemctl disable ssoosshd` is the part a drop-in cannot express: the base
unit's `WantedBy=multi-user.target` has to be undone, or the server tries to
start at boot and fails every time.

Three things in those files are load-bearing:

- **`BindsTo=ssoossh-agent.service`** stops `ssoosshd` when the agent stops.
  Without it an agent that dies leaves a running certificate authority that
  cannot issue certificates, and a stopped unit is far more likely to be
  noticed than a healthy-looking one that fails every signature.
- **`Restart=no` on the agent** is deliberate. A restarted agent comes back
  empty, and that is the state above.
- **`ReadWritePaths=/run/ssoossh`** on `ssoosshd`. The shipped unit sets
  `ProtectSystem=strict`, which makes the whole hierarchy read-only, and
  connecting to a Unix socket needs write access to it.

Starting up, in order:

```bash
systemctl start ssoossh-agent
pass show ssoossh/ca-key |
  sudo -u ssoossh SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -
sudo -u ssoossh SSH_AUTH_SOCK=/run/ssoossh/agent.sock ssh-add -l
systemctl start ssoosshd
```

`ssh-add` runs as `ssoossh` because the agent creates its socket at mode
0600 under its own account, which is also what keeps every other user on
the box away from a signing oracle.

### Delegating those two steps without full root

Loading the key does not start the server. `ssh-add` speaks to the agent
over its socket and knows nothing about systemd, and `BindsTo=` is a
`Requires=`-style dependency: it propagates a start from `ssoosshd` to the
agent, and a stop back the other way, never a start. So `systemctl start
ssoosshd` stays a second, separate action, and it has to come after the key
is in. Run it first and it pulls the agent up empty, then fails on the
missing key.

Both steps can be granted on their own:

```text
# /etc/sudoers.d/ssoossh-agent
Cmnd_Alias SSOOSSH_AGENT = /usr/bin/ssh-add -, /usr/bin/ssh-add -l
Cmnd_Alias SSOOSSH_START = /usr/bin/systemctl start ssoosshd.service

mnestor ALL=(ssoossh) NOPASSWD:SETENV: SSOOSSH_AGENT
mnestor ALL=(root)    NOPASSWD: SSOOSSH_START
```

`NOPASSWD` is not a convenience here. Standard input is the key, and
`ssh ca-host '...'` allocates no terminal, so a rule that prompts for a
password has nowhere to ask. Do not reach for `ssh -t` to supply one: a pty
makes standard input a terminal, which echoes the key into the scrollback
the pipe exists to keep it out of.

`SETENV:` is what permits the inline `SSH_AUTH_SOCK=`. sudoers implies that
tag only when the command matched is `ALL`, so a rule naming `ssh-add` has
to say it, and without it `sudo` refuses before running anything:

```text
sudo: sorry, you are not allowed to set the following environment variables: SSH_AUTH_SOCK
```

`sudo -u ssoossh env SSH_AUTH_SOCK=... ssh-add -` avoids the tag by
permitting `/usr/bin/env` instead, which is a wildcard for running anything
at all as `ssoossh`. Prefer the tag.

The argument lists matter too. sudoers matches them exactly, so the alias
grants the stdin form and the listing and nothing else: not `ssh-add -D`,
which empties the agent and leaves a server that is up and cannot sign, and
not `ssh-add /path/to/key`, which is the disk copy this page exists to
avoid.

:::note[Clients during the window]
While `ssoosshd` is stopped, a client holding a still-valid certificate
reuses it and never contacts the server, so an unexpired session is
unaffected. That works whether the client pinned `capubkey` or is running on
[its cached copy](/ssoossh/reference/client-config/#the-ca-key-cache) of the
key, which is what a client that has ever reached this server has. Only a
client that has never talked to it, or whose certificate expired during the
window, has to wait for the key to be loaded.
:::

## Supported CA key types

The same algorithm policy as everywhere else, plus Ed25519:

| CA key | Works | Notes |
| --- | --- | --- |
| Ed25519 | yes | the only source other than `ssh_key`/`ssh_key_file` that can hold one |
| ECDSA P-256/384/521 | yes | |
| RSA >= 2048 | yes | constrained to `rsa-sha2-512` and `rsa-sha2-256` |
| RSA < 2048 | no | rejected at startup |

RSA is worth a note. An unconstrained agent signer offers `ssh-rsa` first,
which is SHA-1. `ssoosshd` restricts an agent-held RSA key to the SHA-2
algorithms, so a certificate is never signed with SHA-1. A token reached
through the agent inherits the token's own restrictions on top, so Ed25519
on a PKCS#11 token remains unavailable regardless of the agent.

## Failure modes

| Symptom | Cause |
| --- | --- |
| `connect to ssh-agent at ...` | the socket path is wrong, or the agent is not running |
| `ssh-agent holds no keys` | the agent is up but nothing was `ssh-add`ed |
| `ssh-agent holds N keys and ... key_fingerprint is not set` | name the CA key |
| `no key in the ssh-agent matches ...` | the fingerprint is wrong; the message lists what the agent does hold |
| `hsm is configured but this build has no PKCS#11 support` | the default build; use this page's approach, or an `hsm`-tagged build |
