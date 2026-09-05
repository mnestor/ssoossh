---
title: sudo and su
description: Put sudo and su behind a browser approval with pam_ssoossh, without locking yourself out.
eyebrow: Host administration
sidebar:
  order: 3
---

`pam_ssoossh` authenticates a local `sudo` or `su` by generating a single-use
keypair, asking `ssoosshd` to certify it, printing an approval URL, waiting for
a human to approve it in a browser, and validating the certificate that comes
back. Nothing is written to disk, and the certificate is discarded the moment
it has been checked.

:::danger[Read this before editing /etc/pam.d/sudo]
Getting this file wrong costs you `sudo` on that machine -- which is also how
you would normally fix a PAM mistake.

- Open a second root session before you touch the file, and keep it open until
  a third, fresh session has confirmed `sudo` still works.
- Test from that second session, never from the one you are editing in.
- Copy the file first: `cp /etc/pam.d/sudo /etc/pam.d/sudo.bak`. Restoring it
  from the still-open root shell is the recovery path.
- Never put the module in a shared include -- `common-auth`, `system-auth`,
  `base-auth`. Screen lockers, display managers and `polkit` read those too,
  and an approval prompt nobody can see is a lockout.
:::

## Before you start

- The module is installed --
  [Installing pam_ssoossh](/ssoossh/hosts/pam/install/).
- The CA public key is on the host --
  [the trusted CA file](/ssoossh/hosts/pam/trusted-ca/).
- A principals map exists, unless every user's identity provider username is
  spelled exactly like the local account they `sudo` as --
  [the principals map](/ssoossh/hosts/pam/principals-map/). This is the part
  that surprises people: the certificate names the **approver**, never the
  local account that was asked for.

## The sudo stanza

One `auth sufficient` line in `/etc/pam.d/sudo`, above the line that pulls in
password authentication:

```ini
# /etc/pam.d/sudo
auth    sufficient  pam_ssoossh.so server=https://ssoossh.example.com \
                    trusted-ca-file=/etc/ssoossh/ca.pub \
                    principals-map=/etc/ssoossh/principals.yaml \
                    skew-tolerance=2s timeout=60s
auth    required    pam_unix.so
```

The ssoossh line is the same everywhere. What it goes above differs:

| Distribution | The line it goes above |
| --- | --- |
| Debian, Ubuntu | `@include common-auth` |
| RHEL, AlmaLinux, Rocky | `auth include system-auth`. `authselect` owns `/etc/pam.d/system-auth`, so edit `/etc/pam.d/sudo` |
| Alpine | `auth include base-auth` |

The rest of the file stays exactly as your distribution shipped it. On Debian
that means `@include common-account` and
`@include common-session-noninteractive` are untouched below.

:::caution
Only the `auth` management group is implemented. Do not list
`pam_ssoossh.so` on `account`, `password` or `session` lines -- it has no code
for them. `pam_sm_setcred` establishes nothing: the certificate is consumed
during authentication and never installed into the session.
:::

## The su stanza

Same shape, with two neighbours that matter:

```ini
# /etc/pam.d/su
auth    sufficient  pam_rootok.so
# auth  required    pam_wheel.so                 (if your distribution has it)
auth    sufficient  pam_ssoossh.so server=https://ssoossh.example.com \
                    trusted-ca-file=/etc/ssoossh/ca.pub \
                    principals-map=/etc/ssoossh/principals.yaml \
                    timeout=60s
```

Keep `pam_rootok.so` first, so root switching to another account still needs
no approval. Keep any `pam_wheel.so` line your distribution ships above the
ssoossh line: a group restriction on who may `su` at all should not be
bypassable by an approval.

Then the distribution's include -- `@include common-auth` on Debian and
Ubuntu, `auth include system-auth` on RHEL and its rebuilds,
`auth include base-auth` on Alpine.

:::note
`su` sets `PAM_USER` to the account being switched **to**. The certificate has
to authorize that account, so the principals map entry that matters is the
target account's, not the caller's. `alice` running `su - deploy` needs
`deploy: [alice]` in the map.
:::

## Why `sufficient`

`sufficient` means an approval ends the auth stack, and anything else hands
over to the next module -- normally `pam_unix.so`, so a local password still
works.

A denied approval therefore reaches the password prompt. That is the intended
posture rather than an oversight: ssoossh is an additional way to
authenticate, not a gate that can lock an operator out of a host when the
browser flow is unavailable. An outage of the ssoossh server degrades to "no
browser approval available", not "no `sudo` on any host".

`required` or `requisite` inverts that. The whole `auth` group fails when
`ssoosshd` is unreachable, and nothing later in the stack gets a chance to
succeed, so an ssoossh outage becomes a `sudo` outage everywhere. Choose it
only if that trade-off is genuinely wanted -- password `sudo` disallowed for
compliance reasons, say -- and understand that it makes the ssoossh server a
hard dependency of every host's recovery path.

## What each outcome does

| At the terminal | Module returns | With `sufficient` |
| --- | --- | --- |
| Approved, and all four checks pass | `PAM_SUCCESS` | `sudo` proceeds; nothing else in the stack runs |
| The approver denied it | `PAM_AUTH_ERR` | Falls through to the password prompt |
| Nobody answered before `timeout` | `PAM_AUTH_ERR` | Falls through to the password prompt |
| The request expired server-side, the server could not issue, or a check failed | `PAM_AUTH_ERR` | Falls through to the password prompt |
| `ssoosshd` unreachable, or answered with something that was not an answer | `PAM_AUTHINFO_UNAVAIL` | Falls through to the password prompt |
| Ctrl-C at the approval prompt | `PAM_IGNORE` | The module contributes nothing; the stack continues |
| `server` missing, or `pam_get_user` failed | `PAM_USER_UNKNOWN` | Falls through; nothing was ever sent |
| Trusted CA file missing, unreadable, or holding no usable key | `PAM_NO_MODULE_DATA` | Falls through; nothing was ever sent |

Every failure logs its reason to syslog under `LOG_AUTHPRIV`. Nothing is
written to stdout or stderr, because both belong to `sudo`.

The distinction between `PAM_AUTH_ERR` and `PAM_AUTHINFO_UNAVAIL` is
deliberate: a server that answered and said no is an authentication failure; a
server that could not be reached is missing information, which is what lets a
`sufficient` stack fall through cleanly.

## The server side

PAM certificates are their own certificate type, configured under
[`cert_options.pam`](/ssoossh/reference/config/cert_options/pam/):

```yaml
cert_options:
  pam:
    # Who may approve a PAM request at all, deployment-wide.
    require:
      group: staff

    # Validated once by the module and discarded. Seconds, not hours.
    valid_duration: 30s

http:
  cert_request_rate_limit:
    pam: 10           # per second, per source address
```

- [`cert_options.pam.valid_duration`](/ssoossh/reference/config/cert_options/pam/#valid_duration)
  defaults to `30s`. Set it together with the module's `skew-tolerance`: the
  window is short on purpose, and drift between the host and the server eats
  into it. See
  [troubleshooting](/ssoossh/hosts/pam/troubleshooting/#clock-skew).
- [`cert_options.pam.require`](/ssoossh/reference/config/cert_options/pam/#require)
  gates who may approve a PAM request across the whole deployment. Which local
  account an approved certificate then authorizes is the host's decision, made
  against its own `principals-map`.
- [`cert_options.pam.key_id_template`](/ssoossh/reference/config/cert_options/pam/#key_id_template)
  deliberately never falls back to the `user` template, so a `sudo` and an SSH
  login by the same person stay distinguishable in an audit log.
- [`http.cert_request_rate_limit.pam`](/ssoossh/reference/config/http/cert_request_rate_limit/#pam)
  bounds request creation per source address.

## A safe rollout

Do this on one host first, and keep it to one service at a time.

1. **Pick a test host** that is not the one you administer everything else
   from, and confirm you have a way back into it that does not use `sudo` --
   console access, or a root SSH key.
2. **Open a second root session** and leave it open. `sudo -i` in another
   terminal is enough. Do not close it until step 8.
3. **Back up the file**: `cp /etc/pam.d/sudo /etc/pam.d/sudo.bak`.
4. **Check the pieces before wiring them in.** `ssh-keygen -l -f
   /etc/ssoossh/ca.pub` should list your CA key. The principals map should
   name every account that must keep working, including the one you are about
   to test with.
5. **Add the line** above the password include, exactly as above. Save.
6. **Test from a third, fresh terminal**: run `sudo -v`. You should get an
   approval URL. Approve it in a browser and the command should succeed.
7. **Test the fallback.** Press Ctrl-C at the prompt and confirm you reach the
   password prompt and can still authenticate. Then deny an approval in the
   browser and confirm the same. These are the paths that matter during an
   outage.
8. **Read syslog** either way -- `journalctl -t sudo` or
   `grep pam_ssoossh /var/log/auth.log` -- and confirm the version line and
   the outcome are there. Only now close the second root session.
9. **Roll out** with configuration management, one wave at a time, and watch
   for `PAM_AUTHINFO_UNAVAIL` and principals-map warnings in the fleet's logs
   before widening.

Add `debug` to the line while testing. It logs the decision for each of the
four checks, the URL sent to the terminal, and the request context at
`LOG_DEBUG`. Take it off afterwards.

## What it looks like in use

An on-call engineer is paged at 03:00 and needs to restart a service on
`web01`. They SSH in with their ssoossh certificate as usual, then:

```console
$ sudo systemctl restart nginx
Approve this request in your browser:
https://ssoossh.example.com/approve/6f1c0a5e-1f2b-4c3d-8e9f-0a1b2c3d4e5f
```

They open the URL on the laptop they are already signed in on. The approval
page names the machine, the service (`sudo`), the terminal, the account being
authenticated, and the command line -- `sudo systemctl restart nginx` -- all
of it reported by the host and labelled as the host's own claim, alongside the
source address the server observed for itself. They approve; the certificate
is signed, delivered to the still-waiting terminal, checked, and discarded;
`nginx` restarts.

Three things came out of that which a typed password would not have produced.
There was no shared or reused secret on the host. The audit trail names who
approved, from where, and what was granted. And when that engineer leaves the
company, disabling them in the identity provider ends their `sudo` on every
host at once, without touching a single `/etc/pam.d` file.
