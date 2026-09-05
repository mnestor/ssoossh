---
title: Trusting the CA in sshd
description: Point TrustedUserCAKeys at the ssoossh CA public key, map principals, and rotate without an outage.
eyebrow: Host administration
sidebar:
  order: 1
---

The one thing every target host needs. `sshd` is told which certificate
authority to trust, and from then on it accepts certificates that authority
signed instead of keys listed in `authorized_keys`.

## The one line

In `/etc/ssh/sshd_config` on every host that should accept ssoossh
certificates:

```ini
TrustedUserCAKeys /etc/ssh/ca.pub
```

Then reload:

```bash
systemctl reload sshd
```

`/etc/ssh/ca.pub` holds the CA public key in `authorized_keys` format, one key
per line. The path is a convention, not a requirement.

## Getting the CA public key

Fetch it from the running server rather than copying the file you generated
the CA from. That confirms the key the server is actually signing with, which
is the key your hosts have to trust:

```bash
ssoossh --server https://ssh.example.com ca > /etc/ssh/ca.pub
```

`ssoossh ca` prints the CA public key or keys, one per line, in
`authorized_keys` format -- exactly what `TrustedUserCAKeys` wants. The server
serves the same thing over HTTP at `GET /api/ca`, as a JSON envelope; the
command is the convenient form of it.

The key is a public key. It is fine to publish it, put it in a configuration
management repository, and bake it into images.

### Full mode and split mode

The server keeps a registry of the CA public keys its signers have announced,
and `ssoossh ca` reads that registry. Where the key comes from depends on how
you run the server:

| Mode | Where the key comes from |
| --- | --- |
| Full (single instance, or several instances sharing a database) | The instance loads its CA key at startup -- from `ssh_key` or from an HSM -- and registers it. The key is available as soon as the server is up. |
| Split (a separate `ssoosshd sign` process) | The API server starts with no key source. The signer announces its key to the registry over NATS within seconds of starting, so start the API server first, then the signer, then fetch. |

If no signer has registered a key yet, the request fails rather than returning
an empty file. Check before you redirect the output over a file a host is
already using.

## What sshd does with the certificate

Trusting the CA is not the same as accepting every certificate it signs.
`sshd` still:

- verifies the CA signature against `TrustedUserCAKeys`;
- checks the validity window, so an expired certificate is refused with no
  help from ssoossh;
- enforces the critical options the certificate carries, such as
  `source-address` and `force-command`;
- checks that the login name the client asked for is one of the certificate's
  principals, or is allowed by `AuthorizedPrincipalsFile` /
  `AuthorizedPrincipalsCommand`.

## Principals and AuthorizedPrincipalsFile

By default, `sshd` accepts a certificate for a login when the local account
name is one of the certificate's principals. A certificate for `alice` logs in
as `alice`.

When the identity provider's usernames are not the local account names, or a
shared account should be reachable by several people, map them on the host.
Either a static file per account:

```ini
AuthorizedPrincipalsFile /etc/ssh/auth_principals/%u
```

with one principal per line in `/etc/ssh/auth_principals/deploy`, or a command
that answers the same question:

```ini
AuthorizedPrincipalsCommand /usr/bin/ssoossh host principals %u
AuthorizedPrincipalsCommandUser root
```

`ssoossh host principals` implements exactly that contract: it runs as root,
is called on every login attempt, and never touches the network -- it answers
only from the local mapping file, `/etc/ssoossh/principals.json` by default
(`--file` changes it). One principal per line on stdout; an unknown account or
a missing file exits 0 with no output, which `sshd` reads as no principals; a
malformed file exits non-zero. Edit the mapping with `ssoossh host mapping
add`, `list` and `remove`.

:::note
This is a different file from the PAM module's
[principals map](/ssoossh/hosts/pam/principals-map/). `sshd` reads the JSON
mapping through `AuthorizedPrincipalsCommand`; `pam_ssoossh` reads its own
YAML map directly. They answer the same kind of question for two different
programs, and neither one is consulted by the other.
:::

## Rotation with multiple keys

`TrustedUserCAKeys` takes more than one key, and a certificate signed by any
of them is accepted. That is the whole rotation mechanism:

1. Add the new CA public key to the file on every host, as a second line,
   while the server is still signing with the old one. Reload `sshd`.
2. Cut the server over to the new key. Certificates signed with it are
   accepted immediately, and outstanding certificates signed with the old one
   keep working until they expire.
3. Remove the old key from the hosts once nothing signed by it can still be
   valid.

During a rotation the server's registry holds both keys, so `ssoossh ca`
prints both and step 1 is a single redirect on each host. There is no window
in which a host trusts neither key.

Do the same, in the same order, for the PAM module's `trusted-ca-file` -- it
is read on every authentication, so no restart is involved there at all. See
[the trusted CA file](/ssoossh/hosts/pam/trusted-ca/).

To see what a host currently accepts:

```bash
ssh-keygen -l -f /etc/ssh/ca.pub
```

## Revocation, and offboarding

There is no revocation machinery, deliberately. ssoossh certificates are
short-lived -- minutes for an SSH login, seconds for a PAM or console one --
so they expire faster than a revocation list could be distributed to a fleet.
Expiry does the work.

To offboard someone, disable them in the identity provider. They cannot get a
new certificate, the one they hold dies on its own, and nothing on any host
needs touching. That is also the answer for a compromised laptop.

If you need to stop a specific host from accepting anything at all, remove its
`TrustedUserCAKeys` line, or empty the file, and reload `sshd`. That is a host
decision and takes effect on the next login attempt.

`sshd` also supports `RevokedKeys` for the exceptional case, but ssoossh
publishes no KRL and the short lifetimes are the intended control.

## What this does not give you

Host certificates. ssoosshd issues none, because nothing can verify a host's
claim to its own hostname, and unverifiable host identity signed by the same
CA that signs user access is worse than no host identity at all. Host key
verification stays whatever you already do -- `known_hosts`, SSHFP records, or
your own host CA.
