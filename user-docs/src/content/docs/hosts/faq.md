---
title: Host admin FAQ
description: The questions people who run the target hosts ask first.
eyebrow: Host administration
sidebar:
  order: 11
---

For whoever administers the machines people log in to, rather than the ssoossh
server itself.

## Hosts and sshd

### What do I have to configure on my hosts?

One line of `sshd_config`: `TrustedUserCAKeys` pointing at the ssoossh CA
public key, fetched from the running server with `ssoossh ca`. Reload `sshd`
and `authorized_keys` files can go away.

Optionally, map allowed login names with `AuthorizedPrincipalsFile` or
`AuthorizedPrincipalsCommand`. See
[Trusting the CA in sshd](/ssoossh/hosts/sshd-trust/).

Everything beyond that -- `sudo`, `su`, a console login, an SSH second factor
-- is the optional PAM module, which you install and wire in one service at a
time. See [Installing pam_ssoossh](/ssoossh/hosts/pam/install/).

### How do I revoke a certificate, or offboard someone?

You do not revoke, and that is deliberate: certificates are short-lived enough
that expiry does the work a revocation list would. Disable the person in the
identity provider. They cannot get a new certificate, and the one they hold
dies on its own -- within hours for an SSH certificate, within seconds for a
PAM or console one. Nothing on your hosts needs touching.

See [project decisions](/ssoossh/project/decisions/) for why there is no
revocation machinery.

### Can certificates be pinned to a source IP?

Service certificates can, because services sit still. User certificates are
not pinned: people move between office, VPN and hotel wifi, and a short
lifetime already covers the risk.

What the source network does affect is the *lifetime*. A request from the
office range can get a workday; the same laptop on hotel wifi gets minutes.
See [certificate lifetime policy](/ssoossh/operations/certificate-policy/).

`sshd` enforces a `source-address` critical option when a certificate carries
one, so the host side of this needs no configuration either way.

### Can my hosts get certificates too?

No. Nothing can verify a host's claim to its own hostname, and unverifiable
host identity signed by the CA that also signs user access is worse than none.
That may change if a real host-verification mechanism -- something like an ACME
challenge -- lands.

Host key verification stays whatever you already do. The client's
`ssoossh host mapping` and `ssoossh host principals` commands remain, for local
`AuthorizedPrincipalsCommand` mapping; they never talk to the server.

### How do I rotate the CA without an outage?

Both files that hold the CA public key take more than one key, and a
certificate signed by any of them is accepted. Add the new key everywhere
first, cut the server over, then remove the old one once nothing signed by it
can still be valid.

`sshd` needs a reload after the edit; the PAM module reads its file on every
authentication, so it needs nothing. See
[rotation](/ssoossh/hosts/sshd-trust/#rotation-with-multiple-keys).

## The PAM module

### Which version of the module am I running?

Every authentication logs one unconditional line to syslog naming it, along
with the `ssoosshd` release the build was qualified against and the crypto and
HTTP libraries it actually linked:

```text
pam_ssoossh: 1.0.0 | ssoosshd: v1.0.0 | crypto: OpenSSL 1.1.1k | fips: off | http: libcurl/7.61.1 OpenSSL/1.1.1k
```

That line is what makes "which crypto is running inside `sudo`, across the
fleet" a syslog grep rather than guesswork. The fields are described under
[Logging](/ssoossh/hosts/pam/reference/#logging).

### Which services can I put it in?

`sudo`, `su`, `sshd` and a console `login`, one `auth sufficient` line each,
above the service's existing password authentication.

Never a shared include -- `common-auth`, `system-auth`, `base-auth`. Screen
lockers, display managers and `polkit` read those too, and an approval prompt
nobody can see is a lockout.

Only the `auth` management group is implemented. The module has no code for
`account`, `password` or `session` lines.

### Can I use ssoossh for remote logins but keep Touch ID or a smartcard locally?

Yes. Add the [`ssh-only`](/ssoossh/hosts/pam/reference/#ssh-only) argument and
put the local factor after the ssoossh line. A login that did not arrive over
SSH returns `PAM_IGNORE` before anything is generated or sent, so the stack
goes straight on to the next module.

```ini
auth    sufficient  pam_ssoossh.so server=https://ssoossh.example.com \
                    trusted-ca-file=/etc/ssoossh/ca.pub ssh-only
auth    sufficient  pam_tid.so
```

That is the point of the flag: a person at the keyboard has a factor a remote
login cannot use, and someone who arrived over `ssh` has no Touch ID to offer.
It picks which factor to ask for and is not a security boundary. The macOS
stack is written out under
[macOS and Touch ID](/ssoossh/hosts/pam/sudo/#macos-and-touch-id).

### Can pam_ssoossh lock me out of sudo?

Not if you keep the recommended `sufficient` control flag: an unreachable
ssoossh server returns `PAM_AUTHINFO_UNAVAIL` and PAM falls through to the next
module, normally `pam_unix.so`, so a local password still works. Every other
failure path -- a denial, a timeout, Ctrl-C, a broken trusted CA file --
behaves the same way.

What locks people out is the editing. Read the lockout warning in
[sudo and su](/ssoossh/hosts/pam/sudo/), keep a root shell open while you work,
and test from a third terminal. The longer answer is in
[troubleshooting](/ssoossh/hosts/pam/troubleshooting/#can-pam_ssoossh-lock-me-out).

### Does the PAM module store anything on disk?

No. Each attempt uses an ephemeral keypair and a certificate valid for
seconds; both are validated once and discarded. The module reads two files --
the trusted CA file and the principals map -- and writes none.

### sudo approvals fail intermittently after working fine

Check the clock. PAM certificates are valid for seconds
(`cert_options.pam.valid_duration` defaults to `30s`), and drift beyond the
module's `skew-tolerance` (default `2s`) makes validly issued approvals fail
check 4. It presents as intermittent because the two windows still overlap part
of the time.

Run NTP -- `chronyd` or `systemd-timesyncd` -- on every host running
`pam_ssoossh`, and widen `skew-tolerance` only as a last resort, since it also
lengthens the life of a certificate that leaks. Other causes are in
[troubleshooting](/ssoossh/hosts/pam/troubleshooting/#intermittent-failures).

### Why was my login refused after the approval succeeded?

Because the host refused the certificate the server issued, which is almost
always the principals map. A certificate names the **approver's** identity,
never the local account that was asked for, so the map is what authorizes the
two to match -- and once a map loads it is authoritative, so an account with no
entry is denied even when a certificate principal equals its name.

See [the principals map](/ssoossh/hosts/pam/principals-map/).

### Does it work on a machine with no browser at all?

Yes, that is what the console flow is for: a short code and a QR code on the
console screen, carried to any device that does have a browser. `mode=auto`
picks it when `PAM_RHOST` is empty and `PAM_TTY` names a physical terminal, so
the same `pam.d` line does the right thing for a serial console and for an SSH
session.

Console mode is compiled in on Linux and FreeBSD only. See
[console login](/ssoossh/hosts/pam/console/).

### Do I need to open outbound firewall holes?

The module talks to `ssoosshd` over HTTPS, TLS 1.3 only, verified against the
host's own trust store, and holds one streaming connection open while it waits
for the approval. So: outbound 443 to the server, from every host that loads
the module, and no inbound anything.

Redirects are not followed, and there is no option for a private CA bundle
other than installing your CA into the host's trust store. A proxy that
buffers or kills idle connections will break the waiting half of the flow even
where the request itself succeeds.

### Can I lock down client settings across a fleet?

Yes -- an `enforce` YAML file, the Windows registry under
`HKLM\SOFTWARE\Policies`, or macOS managed preferences. All three are
guardrails rather than a security boundary, since the client runs as the user.
See [client settings enforcement](/ssoossh/hosts/client-enforcement/).
