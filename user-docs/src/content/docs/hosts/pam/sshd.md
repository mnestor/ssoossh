---
title: sshd keyboard-interactive
description: Run pam_ssoossh inside sshd, on its own or as a second factor after a public key.
eyebrow: Host administration
sidebar:
  order: 5
---

`sshd` runs PAM through keyboard-interactive authentication, and relays the
module's message to the client, where the person logging in reads it in their
own terminal. That makes an approval usable as an SSH authentication step --
either on its own, or as a second factor behind a public key.

This is always the browser flow. `PAM_RHOST` is set for an SSH session, so
`mode=auto` never picks the console flow here.

## The sshd stanza

```ini
# /etc/pam.d/sshd
auth    sufficient  pam_ssoossh.so server=https://ssoossh.example.com \
                    trusted-ca-file=/etc/ssoossh/ca.pub \
                    principals-map=/etc/ssoossh/principals.yaml \
                    timeout=90s

# then the distribution's include:
#   Debian, Ubuntu        @include common-auth
#   RHEL, Alma, Rocky     auth substack password-auth
#   Alpine                auth include base-auth
```

Some distributions gate the whole `sshd` PAM stack with `pam_nologin.so` or
`pam_listfile.so` before authentication. Leave those where they are -- they are
policy about who may log in at all, and an approval should not bypass them.

## sshd_config

The stanza does nothing until `sshd` is actually consulting PAM's `auth` stack:

```ini
UsePAM yes
KbdInteractiveAuthentication yes
```

On OpenSSH older than 8.7 the second directive is spelled
`ChallengeResponseAuthentication yes`.

Public-key logins are not affected by any of this: `sshd` only consults PAM's
`auth` stack for keyboard-interactive. So on a host where people already log in
with ssoossh user certificates, adding the stanza changes nothing about those
logins.

### As a second factor

To require **both** a valid certificate or key **and** a human approval:

```ini
UsePAM yes
KbdInteractiveAuthentication yes
AuthenticationMethods publickey,keyboard-interactive
```

`sshd` then runs the methods in order: the client proves possession of the key
(or of a certificate signed by
[`TrustedUserCAKeys`](/ssoossh/hosts/sshd-trust/)), and only then is the PAM
`auth` stack run, where `pam_ssoossh` asks for an approval. The comma is a
sequence, not a choice -- both must succeed.

:::caution
With `AuthenticationMethods publickey,keyboard-interactive`, the second factor
is now on the path of every SSH login to this host. If the ssoossh server is
unreachable the module returns `PAM_AUTHINFO_UNAVAIL` and a `sufficient` stack
falls through to whatever comes next -- normally a password prompt. If nothing
in that stack can succeed unattended, an ssoossh outage becomes an SSH outage
on this host. Decide that deliberately, and keep a way in that does not depend
on it.
:::

To offer the approval as an alternative rather than an addition, list two
method sets:

```ini
AuthenticationMethods publickey keyboard-interactive
```

Space-separated sets are alternatives, so either one on its own is enough.

## Testing safely

An `/etc/pam.d/sshd` mistake costs you SSH to the machine, and unlike `sudo`
there may be nothing else listening. Test in this order.

1. **Keep an existing SSH session open**, and confirm you have console or
   out-of-band access as well. An already-authenticated session survives a
   `sshd` restart; a new one is what you are about to risk.
2. **Back up both files** you are touching: `/etc/pam.d/sshd` and
   `/etc/ssh/sshd_config`.
3. **Check the config parses** before restarting anything:

   ```bash
   sshd -t
   ```

   That catches `sshd_config` syntax errors and a bad
   `AuthenticationMethods` value. It does not read `/etc/pam.d/sshd`, so it
   cannot tell you the PAM stack is right.
4. **Try a second `sshd` on another port first**, if you want the PAM stack
   proven before it is in the path of real logins:

   ```bash
   /usr/sbin/sshd -d -p 2222
   ```

   Connect to port 2222 from another machine, watch the debug output, and
   confirm the approval message reaches your client and that an approval logs
   you in.
5. **Reload the real one** only then -- `systemctl reload sshd` -- and
   immediately open a new session from a different terminal. Keep the old
   session open until that succeeds.
6. **Test the failure paths** from that new terminal: Ctrl-C at the approval
   prompt, and a denial in the browser. Confirm you end up somewhere you can
   still authenticate.

If a new session fails and you still have the old one, restore the backups from
it and reload. That is the recovery path, and it only exists while that session
is open.

## What the approver sees

The approval page names the machine, the PAM service (`sshd`), the tty, the
account being authenticated, and the remote host the module reported --
`PAM_RHOST`, so the address the SSH client connected from -- alongside the
source address the server observed for the request itself. Everything the
module reported is a claim by an unauthenticated caller and the page labels it
as one.

That pairing is the useful part on an `sshd` stack: the reported remote host is
where the person logging in says they are, and the observed source address is
where the *host* is. A mismatch between the two is a question worth asking.
