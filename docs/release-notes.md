# Release Notes

For detailed commit history and changes, see the [GitHub releases](https://github.com/mnestor/ssoossh/releases). Each release's notes are generated from the git log at tag time.

## Overview

This document provides a high-level summary of what was accomplished and what was deliberately deferred. For a complete list of changes, commits, and authors, see the [GitHub releases](https://github.com/mnestor/ssoossh/releases).

## What's in the Current Release

**User certificates and PAM.** A person authenticates to a machine with
their identity provider, twice: once to open a shell (`ssh`), once to
become root (`sudo`/`su`). Both are interactive, both are approved by a
human in a browser, both are short-lived, and both retain nothing
afterwards.

- SSH login via the ssoossh client and `ssh_config` (`Match exec` or
  `ProxyCommand`) — see [getting-started.md](getting-started.md).
- `sudo`/`su` via `pam_ssoossh`, reusing the same browser approval flow —
  see [deployment.md §8](deployment.md#8-pam-sudo-and-su).
- Client packages for Linux (`.deb`/`.rpm`), Windows (`.zip`), and macOS
  (`.zip`, quill-signed and notarized).

## Deliberately Deferred (Not Oversights)

Each has a written plan under `docs/` (see the index in [README.md](README.md)). The service and host client commands fail with a clear message rather than hanging. Several of these (admin/auditor roles, lifetime policy, multi-instance) have since landed on the main branch; see [features.md](features.md) for current status.

- **Service certificates** — non-interactive account certificates
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

For details, see the [GitHub releases](https://github.com/mnestor/ssoossh/releases).
