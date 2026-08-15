# Phase 6: Release artifacts and the tag

**Status: planned.** Part of [release-plan.md](release-plan.md).

## Goal

A tag produces installable artifacts for every supported target, and the
release workflow runs on that tag without hand-holding.

## What is already in place

`.goreleaser.yml` was repaired in delivery phase 1 and the build ids point at
the current monorepo layout. Present today:

| id | Target |
| --- | --- |
| `server-linux-build` | `ssoosshd`, linux |
| `linux-build` | client, linux |
| `windows-build` | client, windows |
| `netbsd-build` | client, netbsd |
| `linux-pam-build` | PAM `.so`, linux amd64 and arm64, `skip: true` until phase 3 |

Plus `nfpms` ids `client` and `server`, and `archives` ids `linux-archives`
and `netbsd-archives`.

So this phase is verification and packaging rather than construction. The one
genuinely new thing is getting the `.so` into a package that installs it
where PAM will find it.

## Work

### 1. Confirm the artifact matrix

Decide and record the target list rather than inheriting it. Delivery phase 6
named client linux amd64, arm64, ppc64le, and s390x plus windows amd64;
`netbsd-build` exists in the config and is in nobody's plan.

Either netbsd is supported, in which case it needs to appear in the
documentation and ideally in a smoke test, or it is not, in which case
building it every release is unexplained work. Pick one.

**No macOS.** Out of scope until the Apple developer account is renewed. An
un-notarized macOS binary is effectively undistributable on current macOS, so
shipping one unsigned is worse than shipping none: it looks like a supported
target and fails at first launch.

### 2. Package the PAM module

The part with real content. A `.so` in a tarball is not usable; it has to land
where the PAM stack looks for it, which differs by distribution:

- Debian and Ubuntu: `/lib/x86_64-linux-gnu/security/` or
  `/usr/lib/x86_64-linux-gnu/security/`, arch-dependent.
- RHEL family: `/lib64/security/` or `/usr/lib64/security/`.

Decide whether this is a third nfpm package (`ssoossh-pam`) or part of the
existing client package. **A separate package is the better answer**: the
client is a user-facing binary anyone might install on a laptop, and the PAM
module is a privileged system component installed on servers. Coupling them
means every laptop install drops a module into the auth stack directory.

The package must **not** modify `/etc/pam.d` on install. A package that edits
the authentication stack automatically can lock an operator out of their own
machine, and doing it in a postinst is how that happens without anyone
choosing it. Ship the configuration snippet as documentation and as an
example file; phase 7 writes the runbook.

Record the glibc floor from phase 3 in the package metadata where the format
supports it, so the package manager refuses an install that would fail at
`dlopen` time.

### 3. Verify the release workflow on a tag

`build-release.yaml` referenced a nonexistent `scripts/build-config.sh` and
passed `.goreleaser.yaml` when the file is `.goreleaser.yml`. Delivery phase
1 records this as fixed; confirm it by actually tagging.

Use a pre-release tag against a scratch repository or a `--snapshot` run
first. A release workflow that has never run on a real tag is not verified,
and the first tag is a bad time to find out.

### 4. Version stamping

`.goreleaser.yml` sets `internal/version.Version`, `Commit`, `Date`,
`ApiPath`, and `BuiltBy` via ldflags on every build id, including the PAM
one. Confirm all five arrive in every artifact, the `.so` included, since
cgo and `-buildmode=c-shared` are the case most likely to differ.

`ssoossh version` and `ssoosshd version` should report something a bug
reporter can use. A module that cannot be asked its version is a support
problem, so decide how the `.so` reports one, whether through a logged line
at load, a symbol, or the package version alone.

### 5. Checksums and provenance

Confirm what the release publishes alongside the binaries. Checksums at
minimum. Signing and SBOM are worth a decision now rather than after the
first release, because adding them later changes what users have learned to
expect.

Not a blocker; a recorded decision.

## Exit criteria

- `goreleaser release --snapshot --clean` produces every intended artifact,
  including the PAM `.so` for amd64 and arm64.
- A tag runs `build-release.yaml` to completion and publishes them.
- The deb and rpm install cleanly on a clean container.
- The PAM package places the `.so` where the distribution's PAM stack looks
  for it, and changes no configuration.

## Verification

- Install the client deb on a clean container and complete a login against
  the phase 7 stack.
- Install the server deb on a clean container and start it.
- Install the PAM package on a clean container, add the stack entry by hand
  per the phase 7 runbook, and complete a `sudo`.
- `objdump -T` on the shipped `.so` confirms the glibc floor phase 3 recorded.
- Every artifact reports the tagged version.
- Uninstall each package and confirm nothing is left behind, particularly
  that the PAM package removal does not leave a dangling `/etc/pam.d`
  reference to a module that no longer exists. That failure mode locks people
  out, and it is the reason item 2 refuses to edit those files on install.
