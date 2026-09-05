# Release Notes

Per-release notes are generated from the git log at tag time — see the
[GitHub releases](https://github.com/mnestor/ssoossh/releases) for what
changed in any given version.

This page carries only what lives nowhere else: the build floors, the
revocation stance, and the gaps worth knowing about before you deploy.
For what ssoossh does today, and for what is coming, see
[features.md](../guide/features.md).

## Revocation

Deliberately not included, and not an oversight. Certificates are
short-lived by design — minutes, not years — so they expire faster than a
revocation list could reasonably be distributed. Expiry does that work.
The reasoning is in [decisions.md](decisions.md).

## PAM module: what it links

`pam_ssoossh` links only libraries the operating system already ships, so
which of them is resident in `sudo` is a property of the host rather than of
the module. **OpenSSL 1.1.1 is a hard floor**, set by RHEL 8; a build against
anything older fails at compile time.

Artifacts are built per platform rather than to one lowest common
denominator, which is what the `-glibc-openssl3`, `-glibc-openssl1.1` and
`-musl` names on a release distinguish.

## Client packages

Linux (`.deb`/`.rpm`), Windows (`.zip`), and macOS (`.zip`, quill-signed
and notarized).

## Known gaps

- macOS ships as a signed, notarized `.zip` only — no `.dmg` yet.
- LDAP configuration is parsed but not consumed: setting it has no effect
  on authentication or on issued certificates.
