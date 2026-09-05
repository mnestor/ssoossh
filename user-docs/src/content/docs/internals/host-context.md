---
title: Host context
description: What a PAM or console request reports about the process and machine that asked, and how far each field travels.
eyebrow: Internals
sidebar:
  order: 2
---

A `sudo` or a console login asks ssoosshd for a certificate from a process
on a machine the server has never seen. The approver in the browser has to
decide whether that ask is theirs, and the audit trail has to let a reviewer
place it later against the host's own logs. Both need the same thing: as
much as the module can honestly say about the process and machine asking.

This page is the reference for those fields: what each one is, where
the module reads it, how far it travels, and what it must never be treated
as.

## Trust

Every field here is self-reported by an unauthenticated caller. The module
runs as whatever invoked it, on a host the server cannot verify, and the
request is created before anyone has logged in. So:

- The server bounds each string to 256 bytes on the way in
  (`service.maxContextFieldLen`) and the fingerprint list to 8 entries.
- Nothing here feeds a decision. Principals come from the approver's held
  accounts, lifetime from policy, source restrictions from the
  server-observed address. A hostname or a command line is context for a
  human, not an input to any check.
- The approval page renders each as a claim, and the audit payload carries
  them as the caller's own words.

`source_ip` is the one value in the same block that the server established
itself, and the page shows it as such.

## The fields

| Wire field | Module source | What it tells the approver and the reviewer |
| --- | --- | --- |
| `username` | `PAM_USER` | The account being authenticated: who `sudo` runs as, or the `login:` prompt. Was already sent on every path. |
| `hostname` | `gethostname()` | Which machine. The join key against that host's sudo and sshd logs. |
| `pam_service` | `PAM_SERVICE` | `sudo`, `su`, `sshd`, `login`, `gdm`. Tells a sudo from an su from a password-less SSH stack using the same module. |
| `tty` | `PAM_TTY` | `pts/3` (a terminal window), `tty1` (a virtual console), `ttyS0` (a serial line), or nothing (cron, a service). |
| `remote_host` | `PAM_RHOST` | The peer for network services. Empty for a console; non-empty on a request claiming to be one is flagged on the page. |
| `requesting_user` | `PAM_RUSER` | Who invoked the service, as opposed to `username`. Under `su` or sudo's `targetpw` they differ, and "alice is becoming root" is only readable with both. |
| `process` | `/proc/self/cmdline` (Linux), `KERN_PROC_ARGS` (FreeBSD), absent on macOS | `sudo -i` versus `sudo systemctl restart nginx`. The most useful single line an approver of a sudo can read. |
| `caller_uid`, `caller_pid`, `caller_ppid` | `getuid()`, `getpid()`, `getppid()` | Join keys into the host's auditd or journal. Sent as JSON integers; absent means not reported, since uid 0 is a value. |
| `machine_id` | `/etc/machine-id`, `kern.hostuuid` | Stable across a rename, so the trail follows a machine and two hosts claiming one name are distinguishable. |
| `os` | os-release `PRETTY_NAME`, `uname -sr` | The platform as the host describes itself. |
| `client` | module name and version | `pam_ssoossh-c/<git describe>`. Names the implementation and build that sent the request, so a log can tell releases apart. |
| `mode` | the pam.d `mode=` argument | `auto`, `sudo`, or `console` as configured. The endpoint says which route was taken; together they explain why a request arrived as a console one. |
| `client_time` | the host's clock, RFC 3339 UTC | Skew against the server is what the module's validity check tolerates and what breaks logins when it grows. Recording it makes the mismatch visible before that. |
| `trusted_ca_fingerprints` | SHA256 of each key in `trusted-ca-file` | The module rejects a certificate signed by any other key, so the page can say "this host will not accept what is about to be signed" before the host does. |

The first five existed before this document; the rest were added with it.
Until then the four context fields went only with a console request. The
module now sends everything on both paths, so a `sudo` approval shows the
same detail a console approval does.

## Where each value goes

| Stage | Carries |
| --- | --- |
| `POST /api/certs/pam`, `POST /api/certs/console` | every field, `apitypes.PAMRequestBody` / `ConsoleRequestBody` |
| `certificate_requests` row | every field, one column each (`trusted_ca_fingerprints` as JSON) |
| approval page, `webtypes.RequestDetailResponse` | every field |
| `cert.requested` audit event | every field (`service.fullHostContextDetail`) |
| `cert.claimed`, `cert.approved`, `cert.denied`, `cert.expired`, `cert.sign_failed`, `cert.issued` | the compact set: `username`, `hostname`, `pam_service`, `tty`, `remote_host`, `requesting_user`, `process`, `machine_id`, `client` (`service.hostContextDetail`) |
| signing job, signer, certificate | nothing. The signer never sees the requester. |

`username` and `hostname` on an audit event always come through
`model.CertificateRequest.ReportedIdentity`, which picks the PAM columns for
a PAM or console request and `local_username`/`local_hostname` for a user
one. The two column pairs mean the same thing and a consumer must not pick
one by hand.

## What the decision records

Alongside the host context, an approval now persists what it granted:
`certificate_request_decisions.principals` and `.granted_options`
(JSON), and the narrowed options are written back onto
`certificate_requests.requested_options` the way the enrollment path always
did. The `cert.approved` event carries `principals`, `extensions`,
`force_command`, `source_addresses`, `no_touch_required`, and the approver's
own `approver_ip` and `approver_user_agent`; `source_ip` on every cert event
remains the requester's.

## Events that did not exist before

- `cert.claimed`: the first browser opened the approval page. No actor; the
  user agent is what tells a person from a link scanner.
- `cert.expired`: nobody answered within the type's budget. System event.
- `cert.sign_failed`: an approval produced no certificate, either because
  the signer refused (`error_code` from the reply) or because the stranded
  sweep found the row stuck (`error_code: stranded`).

See `server/service/audit.go` for the taxonomy and the
[signing pipeline](/ssoossh/internals/architecture/) for where each stage
sits.
