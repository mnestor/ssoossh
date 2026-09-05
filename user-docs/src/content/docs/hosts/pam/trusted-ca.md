---
title: The trusted CA file
description: The authorized_keys-format file that decides whose signature pam_ssoossh accepts.
eyebrow: Host administration
sidebar:
  order: 7
---

The file named by `pam_ssoossh`'s `trusted-ca-file` argument lists the
certificate authorities whose signature on a certificate is accepted. It is the
first of the module's four checks, and it is a signature verification -- not a
string comparison against the file's contents.

## Format

The `authorized_keys` public-key format: one key per line, as
`type base64 [comment]`. That is the same line `ssh-keygen` writes to a `.pub`
file and the same one `sshd` reads from `TrustedUserCAKeys`, so the two files
can be the same file. What belongs here is the **public** half of the CA
`ssoosshd` signs with.

Blank lines and lines starting with `#` are ignored. Options before the key
type, as `sshd` allows in `authorized_keys`, are not: a line must start with
the key type.

```text
# CAs ssoosshd signs PAM certificates with. Rotation in progress:
# the ECDSA key is current, the Ed25519 one takes over next week.
ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzODQAAABh... ssoossh-ca-2026
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJDgdf6ZrlMZNcQIVtB0PPlHZxqh/cv1NWNFbfays7RF ssoossh-ca-2027
```

## How to obtain the key

From the running server, which confirms the key it is actually signing with:

```bash
ssoossh --server https://ssh.example.com ca > /etc/ssoossh/ca.pub
```

`ssoossh ca` prints the CA public key or keys, one per line, in exactly this
format. The server serves the same content over HTTP at `GET /api/ca` as a
JSON envelope. During a rotation the server's registry holds every active key,
so the command prints all of them and the redirect above is the whole update.

To see what a host will accept, the fingerprints `ssh-keygen` prints are the
ones the module logs:

```bash
ssh-keygen -l -f /etc/ssoossh/ca.pub
```

## Rotation

The file is read on every authentication, so a CA can be added before the
server starts signing with it and the old one removed afterwards, with no
restart of anything on the host. A file may list several CAs at once and a
certificate signed by any of them passes.

1. Append the new CA public key on every host, while the server still signs
   with the old one.
2. Cut the server over. Certificates signed with the new key are accepted
   immediately.
3. Remove the old key once nothing signed by it can still be valid -- which for
   a PAM certificate is seconds.

Do the same for `sshd`'s `TrustedUserCAKeys`, which also takes several keys;
that one needs a reload. See
[Trusting the CA in sshd](/ssoossh/hosts/sshd-trust/#rotation-with-multiple-keys).

## Key types

Which CA key types work is decided by the crypto the platform ships. Anything
unsupported fails with an error naming the algorithm, never with a vague
signature failure.

| CA key type | Linux, FreeBSD | macOS |
| --- | --- | --- |
| `ecdsa-sha2-nistp256` / `384` / `521` | supported | supported |
| `rsa-sha2-256`, `rsa-sha2-512` | supported | supported |
| `ssh-ed25519` | supported | supported (macOS 14 and later) |
| `ssh-rsa` (SHA-1) | refused by policy | refused by policy |

A key of a type this build's crypto cannot verify is **skipped** with a warning
naming its type and line number, and the rest of the file still applies. That
is deliberate: an Ed25519 CA appended on a Friday must not take every host of a
different platform offline, and must certainly not do it with "not signed by a
trusted CA" as the only explanation.

Under FIPS, Ed25519 is the algorithm a configuration commonly lacks. The module
has the host's OpenSSL verify a known RFC 8032 vector on first use; if that is
refused, an `ssh-ed25519` CA is skipped with a warning naming FIPS and every
other CA type keeps working. See
[FIPS mode](/ssoossh/hosts/pam/reference/#fips-mode).

## Failure modes

| The file | The module does |
| --- | --- |
| Missing, or unreadable | Returns `PAM_NO_MODULE_DATA` before contacting the server |
| Holds no usable key at all | Returns `PAM_NO_MODULE_DATA`, likewise |
| Holds a key of an unsupported type, plus a usable one | Skips the unsupported key with a warning; carries on |
| Holds a line that is present but not a key -- a truncated paste, a base64 typo | Refuses the whole file with an error naming the line. Keys that parsed are **not** used, because a file in that state is more likely mid-edit than intended |

:::caution
`PAM_NO_MODULE_DATA` is returned before any key is generated or any socket
opened, so a broken trusted CA file means the module contributes nothing at
all. Under the recommended `sufficient` control flag that falls through to the
password prompt, which is why an edit here cannot lock a host out -- but it also
means nobody notices until they read the log.
:::

## Limits

Up to 32 keys are read; further lines are ignored with a warning. A file larger
than 32 KiB is refused, and a line longer than 3072 bytes is malformed. Real CA
files are a few hundred bytes.

## Ownership

`/etc/ssoossh/ca.pub` is the path the shipped examples use. Nothing in the
module fixes it. The file should be owned by root and readable by the users of
the services that load the module, which for `sudo` and `sshd` means root
alone.

It is a public key, so nothing is lost if it leaks. The reason for root
ownership is integrity, not secrecy: whoever can write this file decides which
CA the host will trust.
