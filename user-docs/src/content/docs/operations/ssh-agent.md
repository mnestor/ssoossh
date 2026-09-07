---
title: The CA key in an ssh-agent
description: Holding the CA private key in an ssh-agent so it never enters ssoosshd's memory, including HSM-backed keys without a PKCS#11 module in the server.
eyebrow: Server operations
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

| | `ssh_key_agent` | `hsm` |
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
