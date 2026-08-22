# Release Notes

For detailed commit history and changes, see [CHANGELOG.md](../CHANGELOG.md).

## Overview

This document provides a high-level summary of what was accomplished and what was deliberately deferred. For a complete list of changes, commits, and authors, see the [CHANGELOG](../CHANGELOG.md).

## What's in the Current Release

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

## Deliberately Deferred (Not Oversights)

Each has a written plan in `docs/README.md`'s "Planned" and "Designed but deferred" tables. The service and host client commands fail with a clear message rather than hanging.

- **Service certificates** — non-interactive account certificates
- **Host certificates** and principal mapping
- **LDAP enrichment** and account status
- **Admin and auditor roles**
- **Certificate lifetime and source-network policy**
- **Multi-instance safety** and signer process split
- **Client-side TLS pinning**
- **Console-login PAM** and QR-code approval

## Revocation

Deliberately not included. All certificates in this release are short-lived (seconds, not years), expiring faster than revocation lists could reasonably be distributed.

## PAM Module: glibc Floor

- **amd64**: built against `centos:7`, floor glibc **2.17**
- **arm64**: built against `amazonlinux:2`, floor glibc **2.26**

## Known Gaps

- macOS ships as signed, notarized `.zip` only (no `.dmg` yet)

For details, see [CHANGELOG.md](../CHANGELOG.md).
