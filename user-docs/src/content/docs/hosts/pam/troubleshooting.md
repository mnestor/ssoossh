---
title: PAM troubleshooting
description: What each failure looks like in syslog, what each return code means, and how to test without risk.
eyebrow: Host administration
sidebar:
  order: 9
---

`pam_ssoossh` writes nothing to stdout or stderr -- both belong to `sudo` or
`sshd` -- so syslog is the whole diagnostic surface. Start there for every
problem on this page.

## Reading the log

Everything the module logs goes to `syslog(3)` under `LOG_AUTHPRIV`, with a
`pam_ssoossh:` prefix on every line. The syslog tag is whatever host process
loaded it, so filter on the prefix rather than on a tag:

```bash
# systemd
journalctl -t sudo -t sshd -t login --since "10 min ago" | grep pam_ssoossh

# Debian, Ubuntu
grep pam_ssoossh /var/log/auth.log

# RHEL and rebuilds
grep pam_ssoossh /var/log/secure
```

Every attempt logs one unconditional line at `LOG_INFO`, before anything else
happens:

```text
pam_ssoossh: 1.0.0 | crypto: OpenSSL 1.1.1k | fips: off | http: libcurl/7.61.1 OpenSSL/1.1.1k
```

If that line is missing, the module never ran. The stanza is not being reached
-- wrong file, wrong management group, an earlier module already satisfied the
stack, or the `.so` failed to load. Check the module path against
[the install paths](/ssoossh/hosts/pam/install/#where-the-package-puts-things).

Failures log their reason at `LOG_ERR`. A successful authentication logs at
`LOG_INFO`. Adding `debug` to the `pam.d` line adds, at `LOG_DEBUG`, the
decision for each of the four checks, the URL sent to the terminal, and the
request context -- which is what tells you *which* check failed rather than
just that one did. Take it off again afterwards.

## Which return code means what

| Code | Means | Look at |
| --- | --- | --- |
| `PAM_SUCCESS` | All four checks passed | -- |
| `PAM_USER_UNKNOWN` | `pam_get_user` failed, or `server` is not configured | The `server=` argument. Nothing was sent |
| `PAM_NO_MODULE_DATA` | The argument vector could not be read, an argument is malformed or too long, or the trusted CA file is missing or holds no usable key | The `pam.d` line, and `trusted-ca-file`. Nothing was sent |
| `PAM_AUTH_ERR` | The request resolved and the answer was no: denied, expired, the server could not issue, the certificate did not parse, or a check failed. Also a timeout | The per-check debug lines; the server's audit log |
| `PAM_IGNORE` | Ctrl-C at the approval prompt | Nothing. The module contributed nothing and the stack continued |
| `PAM_AUTHINFO_UNAVAIL` | `ssoosshd` could not be reached, or answered with something that was not an answer | Network, DNS, TLS -- see below |
| `PAM_ABORT` | The keypair or the HTTP client could not be constructed | The host. Out of entropy, out of memory, or a broken libcrypto |

The distinction that matters most: **`PAM_AUTH_ERR` means the server answered
and said no; `PAM_AUTHINFO_UNAVAIL` means there was no answer.** Only the
second one is an outage.

## The server is unreachable

The module fails fast on a genuinely unreachable server -- connection refused,
DNS failure -- rather than hanging, and returns `PAM_AUTHINFO_UNAVAIL`.

With the recommended `sufficient` control flag, PAM then falls through to the
next module in the stack, normally `pam_unix.so`, so a local password still
works. An outage of the ssoossh server degrades to "no browser approval
available", not "no `sudo` at all". With `required` or `requisite` the whole
`auth` group fails instead, and an ssoossh outage becomes a `sudo` outage on
every host using it.

What to check, in order:

1. **Name resolution and reachability from the host itself**, not from your
   laptop: `getent hosts ssoossh.example.com`, then
   `curl -sS -o /dev/null -w '%{http_code}\n' https://ssoossh.example.com/api/ca`.
2. **TLS.** The module verifies against the operating system's trust store and
   speaks **TLS 1.3 only**. There is no option for a private CA bundle other
   than installing the CA into that store. A server whose certificate chain the
   host does not trust, or that will not negotiate TLS 1.3, is unreachable as
   far as the module is concerned. `insecure-skip-verify` proves that
   diagnosis; it is not a fix.
3. **Redirects.** They are not followed. If the base URL redirects -- `http` to
   `https`, or a host without the `www`, or a trailing-slash normalisation --
   point `server=` at the final URL.
4. **Proxies.** `libcurl` honours `http_proxy`, `https_proxy` and `no_proxy`,
   but `sudo` normally scrubs those from the environment, so a host that needs
   a proxy has to arrange it in the calling process. A host that must *not* use
   one may be picking up a system-wide setting inside `sshd`.
5. **Timing.** Connecting is bounded at ten seconds; the whole attempt is
   bounded by `timeout` (default `60s`), and the server's own expiry for the
   request bounds it further when that is earlier.

## Clock skew

The single most common cause of "it worked yesterday".

The server issues PAM certificates with a very short validity window --
[`cert_options.pam.valid_duration`](/ssoossh/reference/config/cert_options/pam/#valid_duration)
defaults to `30s` -- and the module applies `skew-tolerance` (default `2s`)
symmetrically to both ends of it. If the host's clock has drifted further than
that tolerance from the server's, check 4 starts refusing certificates that
were validly issued seconds earlier.

It presents as intermittent failure, because the two windows still overlap part
of the time. That is the kind of failure that looks like a bug and is actually
NTP not running.

```bash
timedatectl status          # systemd hosts: look for "System clock synchronized: yes"
chronyc tracking            # chrony: check "System time" offset
```

Run `chronyd`, `systemd-timesyncd` or an equivalent on every host running
`pam_ssoossh`. Raise `skew-tolerance` only as a last resort: a wider window is
also a longer life for a certificate that leaks. If you do raise it, raise it
alongside a decision about `valid_duration`, not instead of one.

The module also reports its own clock to the server with each request, so a
drifting host is visible on the approval page and in the audit trail before it
starts failing logins.

## Intermittent failures

| Symptom | Likely cause |
| --- | --- |
| Fails sometimes, succeeds sometimes, same user and host | Clock skew, above |
| Fails only for some users | The principals map. An account with no entry is denied once a map loads, even when a certificate principal equals its name |
| Fails only for some hosts | Different `trusted-ca-file` contents, or a CA of a type that host's crypto cannot verify -- check for a skip warning naming the algorithm |
| Fails after a CA rotation | The new CA was not appended everywhere. The file is read on every attempt, so this is a content problem, not a restart problem |
| Fails after a config change, logging a warning on every attempt | A configured `principals-map` that cannot be loaded. It falls back to exact-name matching, which is stricter |
| Started failing under load | [`http.cert_request_rate_limit.pam`](/ssoossh/reference/config/http/cert_request_rate_limit/#pam) is per second per source address, so many hosts behind one NAT share a budget |
| Approval never arrives at the terminal | The streaming connection. The module holds one open and reconnects if it drops; a proxy or firewall that buffers or kills idle connections breaks the delivery half while the request itself succeeds |

## Approvals succeed but the login is refused

That is check 3 or check 4 -- the certificate was issued, and the host refused
it. Turn on `debug` and read which check failed.

Check 3 is nearly always the principals map. The certificate names the
**approver's** identity, never the local account that was asked for, so:

- a map that loads is authoritative, and an account with no entry is denied;
- with no map at all, the certificate must carry the exact local account name;
- for `su`, the entry that matters is the **target** account's.

See [the principals map](/ssoossh/hosts/pam/principals-map/).

Check 1 failing with a message naming an algorithm is a CA key type this build
cannot verify, not a bad signature -- see
[certificate algorithms](/ssoossh/hosts/pam/reference/#certificate-algorithms).
Under FIPS, `ssh-ed25519` is the usual one.

## Can pam_ssoossh lock me out?

Not if you keep the recommended control flag. With `sufficient`, every failure
path -- a denial, a timeout, Ctrl-C, an unreachable server, a broken trusted CA
file, a malformed argument -- hands over to the next module in the stack,
normally `pam_unix.so`, so a local password still works. ssoossh is an
additional way to authenticate, not a gate.

What locks people out is the editing, not the module:

- `required` or `requisite` instead of `sufficient`, which makes the ssoossh
  server a hard dependency of `sudo`.
- Putting the module in a shared include -- `common-auth`, `system-auth`,
  `base-auth`. Screen lockers, display managers and `polkit` read those too,
  and an approval prompt nobody can see is a lockout.
- A syntax error in the `pam.d` file, which is a stack failure before the
  module is reached at all.
- Editing `/etc/pam.d/sudo` from the only session you have, which is also the
  session you would need to undo it.

So: second root session open, a backup of the file, and test from a third
terminal.

## A testing procedure

1. **Open a second root session** and leave it open until the end.
2. **Back up** the `pam.d` file you are about to change.
3. **Check the inputs before wiring anything in.**

   ```bash
   ssh-keygen -l -f /etc/ssoossh/ca.pub                      # the CA the host will accept
   ssoossh --server https://ssoossh.example.com ca           # the CA the server signs with
   timedatectl status                                        # the clock
   ```

   Pipe the second command's output through `ssh-keygen -l -f -` and the
   fingerprints should match.
4. **Add the line with `debug`** on it, above the password include.
5. **From a third, fresh terminal**, run `sudo -v`. Approve it. Read the debug
   lines and confirm all four checks are logged as passing.
6. **Test the failure paths from that same terminal**: Ctrl-C at the prompt,
   then a denial in the browser. Both must reach the password prompt.
7. **Simulate the outage** rather than waiting for one. Point `server=` at an
   unroutable address for one attempt and confirm you get
   `PAM_AUTHINFO_UNAVAIL` in the log and a working password prompt at the
   terminal.
8. **Remove `debug`**, confirm once more from a fresh terminal, and only then
   close the second root session.

For `sshd` there is an extra step worth taking: run a second `sshd` on another
port (`/usr/sbin/sshd -d -p 2222`) and prove the stack there before it is in
the path of real logins. See
[sshd keyboard-interactive](/ssoossh/hosts/pam/sshd/#testing-safely).

## What to collect before asking for help

- The version line from syslog, verbatim. It names the module version and the
  crypto and HTTP libraries actually linked into the process, which is the
  first thing anyone will ask for.
- The `pam.d` line, with the server URL redacted if you must.
- The `LOG_ERR` line for the failure, and the `LOG_DEBUG` lines around it.
- `ssh-keygen -l -f` on the trusted CA file, and `timedatectl status`.
- Whether the same attempt succeeds from a different host and for a different
  user, which separates a host problem from a policy one.
