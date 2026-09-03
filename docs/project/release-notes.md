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

## PAM module: glibc floor

The PAM module is cross-compiled with `zig cc` against a deliberately old
glibc target so it loads on long-lived server distributions:

- **amd64**: `x86_64-linux-gnu.2.17`, floor glibc **2.17**
- **arm64**: `aarch64-linux-gnu.2.26`, floor glibc **2.26**

## Client packages

Linux (`.deb`/`.rpm`), Windows (`.zip`), and macOS (`.zip`, quill-signed
and notarized).

## Known gaps

- macOS ships as a signed, notarized `.zip` only — no `.dmg` yet.
- LDAP configuration is parsed but not consumed: setting it has no effect
  on authentication or on issued certificates.
