# Release notes (draft)

Draft content for the first tagged release's notes; update the version/date
once cut.

## What's in this release

**User certificates and PAM.** A person authenticates to a machine with
their identity provider, twice: once to open a shell (`ssh`), once to
become root (`sudo`/`su`). Both are interactive, both are approved by a
human in a browser, both are short-lived, and both retain nothing
afterwards.

- SSH login via the ssoossh client and `ssh_config` (`Match exec` or
  `ProxyCommand`) — see [getting-started.md](getting-started.md).
- `sudo`/`su` via `pam_ssoossh`, reusing the same browser approval flow —
  see [deployment.md §7](deployment.md#7-pam-sudo-and-su).
- Client packages for Linux (`.deb`/`.rpm`), Windows (`.zip`), and macOS
  (`.zip`, quill-signed and notarized).

## What this release deliberately does not contain

Not oversights — each has a written plan (see `docs/README.md`'s "Planned"
and "Designed but deferred" tables). The service and host client commands
fail with a clear message rather than hanging or producing a stack trace.

- [ ] **Service certificates** — a certificate for a non-interactive
      account (a scheduled job, a file transfer, an automated process)
      rather than a person at a keyboard. Enrolled once against a
      persistent keypair, then reissued unattended on every run with no
      browser and no human in the loop. See
      [what-ssoossh-is.md](what-ssoossh-is.md#certificate-types).
- [ ] **Host certificates** and principal mapping.
- [ ] **LDAP enrichment and account status.**
- [ ] **Admin and auditor roles.**
- [ ] **Certificate lifetime and source-network policy** beyond the flat
      per-type `valid_duration` already in place.
- [ ] **Multi-instance safety** and the signer process split.
- [ ] **Client-side TLS pinning.**
- [ ] **Console-login PAM module** and QR-code approval.

## Revocation

There isn't any, and that's a deliberate trade rather than a gap. Every
certificate type in this release is short-lived — user and PAM
certificates measured in seconds, not the year-long lifetimes a service or
host certificate would carry. They expire faster than a revocation list
could realistically be distributed, so the usual argument against skipping
revocation doesn't apply here.

## PAM module: glibc floor

`pam_ssoossh.so` is built against old-glibc containers specifically so it
loads on older distributions than the build host runs:

- **amd64**: built against `centos:7`, floor glibc **2.17** (highest
  required `GLIBC_` symbol verified at 2.3.2, well under the pinned
  image's floor).
- **arm64**: built against `amazonlinux:2`, floor glibc **2.26**.

Both were load-tested on `debian:bookworm-slim` (glibc 2.36) as a
different-glibc sanity check, via `pam_ssoossh/testing/pamtest.c`. If your
target's `ldd --version` reports older than the floor for its
architecture, the module will not load.

## Known gap

macOS ships as a signed, notarized `.zip` only — no `.dmg`. See
[getting-started.md](getting-started.md) for the install path.
