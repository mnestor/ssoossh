---
title: Release notes
description: Build floors, the revocation stance, and the gaps worth knowing about before you deploy.
eyebrow: Project
sidebar:
  order: 1
---

Per-release notes are generated from the git log at tag time. For what
changed in any given version, see the GitHub releases:

- Server and client: https://github.com/mnestor/ssoossh/releases
- The PAM module (`pam_ssoossh`):
  https://github.com/mnestor/ssoossh-pam/releases

This page carries only what lives nowhere else: the build floors, the
revocation stance, and the gaps worth knowing about before you deploy.
For what ssoossh does today, and for what is coming, see
[How it works](/ssoossh/concepts/) and the [roadmap](/ssoossh/project/roadmap/).

## Revocation

Deliberately not included, and not an oversight. Certificates are
short-lived by design -- minutes, not years -- so they expire faster than a
revocation list could reasonably be distributed. Expiry does that work.
The reasoning is in [Decisions](/ssoossh/project/decisions/).

## PAM module: what it links

`pam_ssoossh` links only libraries the operating system already ships, so
which of them is resident in `sudo` is a property of the host rather than of
the module. **OpenSSL 1.1.1 is a hard floor**, set by RHEL 8; a build against
anything older fails at compile time.

Artifacts are built per platform rather than to one lowest common
denominator, which is what the `-glibc-openssl3`, `-glibc-openssl1.1` and
`-musl` names on a release distinguish. See
[Installing pam_ssoossh](/ssoossh/hosts/pam/install/).

## Client packages

Linux (`.deb`/`.rpm`), Windows (`.zip`), and macOS (`.zip`, quill-signed
and notarized).

## Known gaps

- macOS ships as a signed, notarized `.zip` only -- no `.dmg` yet.
- LDAP configuration is parsed but not consumed: setting it has no effect
  on authentication or on issued certificates.

:::caution
The LDAP entry above is the release-notes source's own wording and looks
stale. [LDAP enrichment](/ssoossh/operations/ldap/) describes enrichment,
the background sync, and group capture as implemented, and names the files
that implement them. Check the release you are running before relying on
either statement.
:::
