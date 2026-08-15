# Phase 6: Release artifacts and the tag

**Status: implemented**, with one gap recorded plainly at the end of item 3.
Part of [release-plan.md](release-plan.md).

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
| `linux-pam-build-amd64` | PAM `.so`, linux amd64 |
| `linux-pam-build-arm64` | PAM `.so`, linux arm64 |

Plus `nfpms` ids `client` and `server`, and `archives` ids `linux-archives`
and `windows-archives`.

So this phase is verification and packaging rather than construction. The one
genuinely new thing is getting the `.so` into a package that installs it
where PAM will find it.

## Work

### 1. Confirm the artifact matrix

**Done: netbsd dropped.** It appeared nowhere in release-plan.md's scope
table, decisions table, or exit criteria ("A tagged release publishes
working client, server, and PAM artifacts for Linux, plus a Windows
client") — this doc's own framing of it as "in nobody's plan" turned out to
be exactly right once checked against the plan it's part of. `netbsd-build`
and `netbsd-archives` are removed from `.goreleaser.yml`. The matrix is now:
client and server for linux (amd64, arm64, ppc64le, s390x), client for
windows (amd64), and the PAM module for linux (amd64, arm64) only.

**No macOS.** Unchanged — still out of scope until the Apple developer
account is renewed.

### 2. Package the PAM module

**Done.** `ssoossh-pam` is a separate nfpm package, per the reasoning
already recorded here — split into four ids (`pam-deb-amd64`,
`pam-deb-arm64`, `pam-rpm-amd64`, `pam-rpm-arm64`) rather than one block
covering both formats and both arches, for two reasons discovered by
actually building it:

- `dependencies` and `libdirs`/`bindir` are **not** template-rendered by
  goreleaser 2.17.1. A single id with `{{ if eq .Arch "arm64" }}` in either
  field shipped the literal, unrendered template string — in the installed
  path the first time, in the `Depends:` control field the second. Splitting
  by arch means every field is a plain literal, which is the one thing
  confirmed to work.
- `bindir` is the wrong field for a `buildmode: c-shared` artifact in the
  first place. nfpm treats it as a Library, not a Binary, and routes it
  through `libdirs.cshared` instead — with `bindir` set and no `libdirs`,
  the `.so` landed at the unrelated default `/usr/lib/pam_ssoossh.so`,
  ignoring `bindir` entirely. Confirmed with an isolated scratch build
  before touching the real config.

Install paths, verified by unpacking the built packages:

| Format | Arch | Path |
| --- | --- | --- |
| deb | amd64 | `/usr/lib/x86_64-linux-gnu/security/pam_ssoossh.so` |
| deb | arm64 | `/usr/lib/aarch64-linux-gnu/security/pam_ssoossh.so` |
| rpm | amd64 | `/usr/lib64/security/pam_ssoossh.so` |
| rpm | arm64 | `/usr/lib64/security/pam_ssoossh.so` |

Each package also declares a glibc floor as a dependency (`libc6 (>= 2.17)`
/ `(>= 2.26)` for deb, `glibc >= 2.17` / `>= 2.26` for rpm), matching the
pinned build images below.

**Does not touch `/etc/pam.d`**, confirmed on both a clean `debian:
bookworm-slim` and a clean `rockylinux:9` container: install, list package
contents, list `/etc/pam.d/`, nothing added. `docs/pam.d-sudo.example`
ships in the package under `/usr/share/doc/ssoossh-pam/examples/sudo` as
documentation only; phase 7 owns the runbook that tells an operator to copy
it.

**The glibc floor gap phase 3 left open is now closed.** Phase 3 built
`linux-pam-build` (now `-amd64`/`-arm64`) with `CC: x86_64-linux-gnu-gcc` /
`aarch64-linux-gnu-gcc`, which link against *this build host's own* glibc
(2.34–2.41 in this devcontainer), not against the pinned old images —
recorded there as real work still to do. Closed here:
`scripts/build-pam-release-so.sh`, wired as a `hooks.post` on each
`linux-pam-build-*` id, overwrites goreleaser's own output by compiling a
second time inside the pinned image for that arch:

- amd64: runs natively inside `centos:7` (glibc 2.17). yum's repos are dead
  (CentOS 7 is EOL); the script points `CentOS-Base.repo` at
  `vault.centos.org` first.
- arm64: runs inside `amazonlinux:2` (glibc 2.26) under qemu emulation on
  this amd64 host. The script registers the binfmt handler itself
  (`tonistiigi/binfmt --install arm64`) rather than assuming it's already
  there, since `/proc/sys/fs/binfmt_misc` isn't reliably visible from
  inside a nested devcontainer even when the host kernel has it.

Phase 3's "what's still open" flagged one more thing for this phase to
check first: a goreleaser warning, `artifact already present in the list
name=pam_ssoossh.so`, because the amd64 and arm64 outputs share a bare
filename disambiguated only by directory. It still fires (both builds
literally produce a file named `pam_ssoossh.so`, in different `dist/`
subdirectories) — checked, and it's cosmetic: nfpm packaging picked the
correct arch-specific file every time, confirmed by unpacking each package
and checking both the binary size and the highest required `GLIBC_` symbol
per arch (table below) rather than trusting the filename alone.

Neither container gets a bind mount of the source tree: this runs under
docker-outside-of-docker, where the daemon is the real host's and its
filesystem paths don't match the devcontainer's
([docs/docker-setup.md](docker-setup.md)). The script instead streams the
tree in and the binary back out via `docker exec`/`docker cp`, which are
resolved client-side and so work the same whether the daemon is local (a
plain CI runner) or remote (docker-outside-of-docker) — no host-path
translation needed either way. A per-arch named Docker volume holds the Go
module cache across runs.

Verified with `objdump -T` / `aarch64-linux-gnu-objdump -T` on the built
`.so`, highest required `GLIBC_` symbol version:

| Arch | Highest required GLIBC symbol | Floor (pinned image) |
| --- | --- | --- |
| amd64 | 2.3.2 | 2.17 (`centos:7`) |
| arm64 | 2.17 | 2.26 (`amazonlinux:2`) |

Both comfortably under their floor. Confirmed live, not just statically:
installed the packaged (dpkg-placed, not manually copied) `.so` on a clean
`debian:bookworm-slim` container, configured a dedicated test PAM service
(`pam_ssoossh/testing/pamtest.c`, same harness phase 3's verification
section names), and ran it. The module loaded, ran, logged its own version
line (item 4), and failed closed cleanly on a missing CA file — proving
`dlopen` and cgo linkage work against a real, different glibc (bookworm's
2.36), not just that `objdump` finds no unresolved symbol.

### 3. Verify the release workflow on a tag

`build-release.yaml`'s `-f .goreleaser.yml` / no `scripts/build-config.sh`
reference is confirmed correct by reading the file directly — the bug this
item describes is already gone.

**Done, with a gap worth stating plainly:** `goreleaser release --snapshot
--clean` was run repeatedly end-to-end in this devcontainer (not once —
each fix above was verified by rerunning it) and succeeds, producing every
archive, every `nfpm` package including all four `ssoossh-pam` variants,
and a checksums file (item 5). This is the verification path this doc
itself names as the thing to do "first" before a real tag. What is **not**
done: an actual `v*` tag was not pushed, so `build-release.yaml` has not
run in real GitHub Actions end to end. Doing that touches shared/remote
state (a real push, a real GitHub Actions run, a real GitHub release) and
needs the user's explicit go-ahead, which this phase did not seek. The
workflow's own steps are otherwise identical to the snapshot command
already verified (`goreleaser release --clean -f .goreleaser.yml
--snapshot=...`), so the remaining risk is narrow: CI-runner-specific
environment differences (a fresh Go module cache, `docker`'s availability
and version on `ubuntu-latest`, whether the qemu binfmt registration step
in `build-pam-release-so.sh` behaves the same on a GitHub-hosted runner).
First real tag should still be treated as the first real test of this,
per the user's own plan to build out `test-release` CI tooling around it.

### 4. Version stamping

**Done**, and one correction to this document: item 4 originally named
`ApiPath` as a fifth field alongside `Version`, `Commit`, `Date`, and
`BuiltBy`. Grepped for it across `internal/version` and the rest of the
tree — it does not exist anywhere and never did in this branch. Struck;
there are four fields, not five, and all four were confirmed.

`Version`, `Commit`, `Date`, and `BuiltBy` reach the `.so`:
`scripts/build-pam-release-so.sh` builds its own `-ldflags` string from the
same four `-X` substitutions as the `*build-ldflags` anchor in
`.goreleaser.yml`, using the `{{.Version}}`/`{{.Commit}}`/`{{.Date}}` values
goreleaser's hook template resolves for that target (empirically confirmed
these resolve inside `hooks.post` — plausible from the docs but verified
directly with a scratch build before relying on it). Confirmed present with
`strings` on the built `.so` and, more convincingly, live: the PAM module's
own log line (below) printed the correct version/commit/date/builtby when
run through a real PAM stack.

**`ssoossh version` and `ssoosshd version` now exist — neither did before
this phase.** That was the actual gap behind "a module that cannot be asked
its version is a support problem": the *binaries* couldn't either. Added:

- `client/cmd/version.go` — a `simplecobra.Commander` that deliberately
  does not embed `simpleCommand`, because `simpleCommand.Run` always checks
  the root's `InitErr` first (`client/cmd/simplecommand.go`). Every other
  leaf command needs `RootCommand.PreRun` to have succeeded (config loaded,
  API client built, CA fetched); a diagnostic command has to keep working
  when that's exactly what's broken. Covered by
  `TestVersionCommandIgnoresInitErr` (`client/cmd/exec_test.go`), which
  mirrors the existing `TestExecuteSurfacesInitErr` fixture with a failing
  config but asserts `version` succeeds anyway. `client/CLAUDE.md`'s
  documented CLI surface is updated to include it.
- `server/cmd/cmd.go` gains a plain `cobra.Command` subcommand. Covered by
  `TestNewCommand_ShouldRegisterVersionCommand` and
  `TestNewCommand_VersionShouldRunWithoutBootstrap` in
  `server/cmd/cmd_test.go` — the latter exists specifically to prove it
  doesn't need `--config` or a reachable OIDC provider.

Both verified for real: installed the built `.deb`s on `debian:
bookworm-slim` and the `.rpm`s on `rockylinux:9`, ran `ssoossh version` and
`ssoosshd version` on each. `ssoosshd version` prints `ssoosshd`, not
`version.Name` (`"ssoossh"`, the shared project identifier used for
logging/observability tags across all three binaries) — the one place this
phase deliberately didn't reuse that constant.

**How the `.so` reports its version:** a logged line, not a symbol or the
package version alone. `pam_ssoossh.go`'s `authenticate()` now logs
`pam_ssoossh <version> (commit <commit>, built <date> by <builtby>)` at
Info on every invocation — not gated behind the module's `debug` argument,
since a module that only reports its version when a config flag is already
set is a worse support problem than one extra log line per `sudo`/`su`
attempt. Verified live via the `pamtest.c` run in item 2: the line appeared
with the correct values before the module failed closed on the missing CA
file.

### 5. Checksums and provenance

**Done — recorded, not blocked.** No `checksums:` block is needed:
goreleaser's default (SHA256, `{{.ProjectName}}_{{.Version}}_checksums.txt`,
covering every binary, archive, and linux package with no `ids` filter)
already runs and was confirmed present after a real snapshot build,
covering all 26 artifacts including all four PAM packages.

Decision: **checksums only for this release.** Signing (cosign, or `quill`
for the eventual macOS artifacts) and SBOM generation are deferred, not
implemented, matching this document's own framing that a decision now is
what's required, not the pipeline itself. Revisit alongside phase 10's
macOS/notarization work — `quill` already needs wiring for that, and
extending the same pass to cover signing/SBOM for the Linux and Windows
artifacts is cheaper done once than twice.

## Exit criteria

- ✅ `goreleaser release --snapshot --clean` produces every intended
  artifact, including the PAM `.so` for amd64 and arm64 — run repeatedly,
  succeeds.
- ⚠️ A tag runs `build-release.yaml` to completion and publishes them —
  the workflow is verified correct by inspection and its equivalent
  goreleaser invocation is verified by repeated local runs; a real tag push
  through GitHub Actions itself was deliberately not performed (see item
  3's gap).
- ✅ The deb and rpm install cleanly on a clean container — verified for
  client, server, and PAM (amd64) and for PAM alone (arm64, under qemu
  emulation) on both `debian:bookworm-slim` and `rockylinux:9`.
- ✅ The PAM package places the `.so` where the distribution's PAM stack
  looks for it, and changes no configuration — verified on both distros,
  both arches.

## Verification

- ⚠️ Install the client deb on a clean container and complete a login
  against the phase 7 stack — installed cleanly, `ssoossh version` runs; a
  full login needs phase 7's server/IdP/browser stack, which doesn't exist
  yet. Deferred to phase 7's own rehearsal, not re-scoped into this phase.
- ⚠️ Install the server deb on a clean container and start it — installed
  cleanly; starting it without a configured DB/OIDC issuer fails fast with
  a clear error (`failed to initialize services: ssh: no key found`)
  rather than crashing silently or hanging. A full start needs phase 7's
  config and stack.
- ✅ (proxy) Install the PAM package on a clean container, add the stack
  entry by hand, and complete a `sudo` — the `sudo` itself needs phase 7's
  stack, so instead exercised the identical code path via
  `pam_ssoossh/testing/pamtest.c` against a dedicated test PAM service
  (`ssoossh-test`, not `sudo`) on the packaged, dpkg-installed `.so`:
  `dlopen` succeeds, the module runs its full path up to the network call,
  logs its version, and fails closed cleanly. This is the same proxy
  verification phase 3's own Verification section already established as
  sufficient before phase 7's stack exists.
- ✅ `objdump -T` on the shipped `.so` confirms the glibc floor phase 3
  recorded — amd64 highest required symbol `GLIBC_2.3.2` (floor 2.17),
  arm64 highest required symbol `GLIBC_2.17` (floor 2.26). Both well
  within.
- ✅ Every artifact reports the tagged version — client and server via the
  new `version` subcommand, the PAM `.so` via its own log line, all
  confirmed on installed packages. ("Tagged" is the snapshot version string
  until item 3's gap closes; the mechanism is identical either way.)
- ✅ Uninstall each package and confirm nothing is left behind — verified
  on both distros for all three packages on amd64, and for PAM alone on
  arm64 (under qemu emulation). `dpkg` correctly leaves the
  shared multiarch `security/` directory in place (other system PAM
  modules live there) but removes `pam_ssoossh.so` itself and the package
  from its database; no dangling `/etc/pam.d` reference on either distro.
  `client`/`server` show `dpkg -l` status `rc` (config files intentionally
  kept, `type: "config|noreplace"`) — expected, not a leftover.
