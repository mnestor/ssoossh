# Phase 3: PAM build environment

**Status: implemented.** Part of [release-plan.md](release-plan.md).
`make pam`, `make test-pam`, and `make lint-pam` all pass in this
devcontainer (with `libpam0g-dev` installed, matching the Dockerfile
change), and both the amd64 and arm64 `linux-pam-build` targets build
through `goreleaser build --single-target` and through a full
`goreleaser release --snapshot --clean`. `docker run hello-world` succeeds
in the rebuilt devcontainer, confirming `docker-outside-of-docker` actually
starts a container. Verified 2026-08-15 after a devcontainer rebuild. The
`pam` job in `.github/workflows/build-test.yaml` runs all three on every
pull request but has not yet run in real CI (nothing in this branch has
been pushed). See "What's still open" at the end of this document.

## Goal

Make `pam_ssoossh` buildable, testable, and lintable: locally in the
devcontainer, in CI, and in the release pipeline against an old enough glibc
to be worth distributing.

## The blocker is narrower than the old plan says

The project's original delivery plan described this as blocked on an amd64
dev VM, later amended to docker-outside-of-docker (see
[release-plan.md](release-plan.md)'s corrections table). Measured against the
current container, neither is what stops a local build:

```console
$ uname -m
x86_64
$ ldd --version | head -1
ldd (Debian GLIBC 2.41-12+deb13u3) 2.41
$ CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared -o /tmp/pam.so ./pam_ssoossh/
# github.com/mnestor/ssoossh/pam_ssoossh
pam_ssoossh/pam.go:7:10: fatal error: security/pam_ext.h: No such file or directory
```

`libpam0g` is installed; `libpam0g-dev`, which ships the headers, is not.
CGO is also off by default in this environment, which produces the more
confusing `-buildmode=c-shared requires external (cgo) linking, but cgo is
not enabled` if you try without setting it.

So there are **two separate problems**, and conflating them is what has kept
this phase looking bigger than it is:

| Problem | What it needs | Blocks |
| --- | --- | --- |
| Cannot compile the module at all | `libpam0g-dev` and `CGO_ENABLED=1` | Local development, unit tests, lint |
| Cannot compile against an old glibc | An old-glibc container, so docker-outside-of-docker | Release artifacts only |

The first is a devcontainer package and unblocks phases 4 and 5 immediately.
The second is genuinely container work and is only needed by phase 6. Do the
first today.

Why the second matters at all: PAM modules link against the system C library,
and a module built against glibc 2.41 will not load on a target with an older
one. The failure is at `dlopen` time inside the auth stack, which is a
uniquely bad place to discover a packaging mistake.

## Work

### 1. Unblock local builds

- Add `libpam0g-dev` to the devcontainer image, and the aarch64 cross
  equivalent if the cross build is to be exercised locally.
- Set `CGO_ENABLED=1` where the PAM targets need it rather than globally.
  The rest of the tree builds without cgo and should keep doing so.
- Replace the `make pam` stub, which currently prints "not buildable here"
  and exits 1, with a real `-buildmode=c-shared` build.
- Replace the `make test-pam` stub, which currently prints "skipping" and
  exits 0. A test target that passes by not running is worse than one that
  fails.

`make lint-pam` already runs `golangci-lint run --build-tags pam
./pam_ssoossh/...` and is the one target here that is real today.

### 2. Wire docker-outside-of-docker

The devcontainer has no `docker` binary (`command -v docker` is empty). This
is the piece the old plan correctly identified, and it is shared work: the
release build needs it, and it is the same mechanism a local release
rehearsal would use.

Mount the host socket into the devcontainer and confirm a container can be
started from inside it. This is the standard devcontainer feature, so the
work is configuration rather than invention.

**Done:** `ghcr.io/devcontainers/features/docker-outside-of-docker:1` added
to `.devcontainer/server/devcontainer.json` and
`.devcontainer/client/devcontainer.json`. The host socket forward stays in
`.devcontainer/docker-compose.local.yml` (gitignored, host-specific, the
same mechanism already used there for other host paths) targeting
`/var/run/docker-host.sock` -- the feature's `socketPath` default, whose
entrypoint does the gid fix-up a plain `/var/run/docker.sock` mount skips.
`docs/docker-setup.md` records this host's socket as rootless docker; see
`.devcontainer/docker-compose.local.yml.example` for the pattern.
**Verified** after the 2026-08-15 devcontainer rebuild: `docker version`
shows a working client and server, and `docker run --rm hello-world`
completes, confirming a container starts from inside the devcontainer.

### 3. Choose and pin the old-glibc images

`.github/workflows/TODO.md` already names candidates:

- **amd64**: `centos:7` or `oraclelinux:7`
- **aarch64**: `amazonlinux:2`, or a native RHEL7-aarch64 image if available

Both are past end of life, which is the point: they are the oldest glibc
anyone plausibly runs a PAM stack on. Pin by digest, not tag. An image that
silently moves is how a reproducible build stops being one, and these
particular images will not be getting fixes.

Record the resulting glibc floor in the release notes. "Requires glibc 2.17
or newer" is a supportable statement; "built on whatever the runner had" is
not.

**Done:** `centos:7` for amd64 (glibc 2.17) and `amazonlinux:2` for aarch64
(glibc 2.26), both pinned by digest next to `linux-pam-build` in
`.goreleaser.yml`. Digests were resolved live against the Docker Hub
registry API on 2026-08-15, not recalled. Neither image's toolchain is
actually used by the build yet -- see item 5's note on the gap this leaves.

### 4. Rewrite `scripts/build-env-for-pam.sh`

Currently malformed: loose package-name fragments, a stray `yum install`
pasted outside any command, and `dpkg --add-architecture x86-64`, which is
invalid, with the following line already doing `amd64` correctly.

It is untracked, so this is closer to writing it than fixing it. Decide what
it is for first. If the container images carry their own toolchain, a script
that installs cross-compilers on the host may not need to exist at all.

**Done:** rewritten. Its job is bootstrapping a bare host that isn't the
devcontainer -- GitHub-hosted CI runners and an amd64 dev VM/checkout --
since the devcontainer gets the same packages baked into
`.devcontainer/Dockerfile` directly. `.github/workflows/build-test.yaml`'s
new `pam` job (item 6) calls it.

### 5. Re-enable the goreleaser build

`.goreleaser.yml:31-32` has `id: linux-pam-build` with `skip: true`. The rest
of the block is already correct: `buildmode: c-shared`, `dir: ./pam_ssoossh/`,
`flags: [-tags=nomsgpack,pam]`, `CGO_ENABLED=1`, linux amd64 and arm64, and
per-arch `CC` overrides (`x86_64-linux-gnu-gcc`, `aarch64-linux-gnu-gcc`)
with matching `PKG_CONFIG_PATH`.

Remove `skip: true` and confirm the cross settings work. Note that phase 6
owns whether the `.so` reaches the nfpm packages; this item is only about it
building.

**Done, with a gap worth stating plainly:** `skip: true` is removed, and
`GOOS=linux GOARCH=amd64 goreleaser build --single-target --id
linux-pam-build --clean --snapshot` succeeds in this devcontainer, producing
`pam_ssoossh.so` and `pam_ssoossh.h`. `objdump -T` on that binary shows
`GLIBC_2.34` as its highest required symbol version -- because `CC:
x86_64-linux-gnu-gcc` on this Debian host is the *native* compiler under its
multiarch name, linking against this host's own glibc (2.41 in the
devcontainer, whatever a CI runner has otherwise), not against item 3's
pinned images. The cross settings were never wired to those images at all:
doing so needs either a sysroot extracted from the pinned image or the link
step run inside it via docker-outside-of-docker (item 2), and neither is
implemented here. This item's own exit criterion ("builds ... for amd64 and
arm64") is satisfied as written; the glibc-2.17-floor claim in item 3 is
not, yet. Recorded here so it isn't silently assumed done -- see this
phase's Verification section, and phase 6
([release-phase6-artifacts.md](release-phase6-artifacts.md)), which owns
whether the released `.so` is correct.

**arm64, verified 2026-08-15 after the devcontainer rebuild:** with
`libpam0g-dev:arm64`, `libc6-dev-arm64-cross`, and `gcc-aarch64-linux-gnu`
now installed, `GOOS=linux GOARCH=arm64 goreleaser build --single-target
--id linux-pam-build --clean --snapshot` succeeds, producing an arm64
`pam_ssoossh.so`. `aarch64-linux-gnu-objdump -T` on it shows `GLIBC_2.34`
as its highest required symbol version -- the same gap as amd64, and for
the same reason (`CC: aarch64-linux-gnu-gcc` is this host's cross
compiler, linking against the cross libc package's glibc, not against
item 3's pinned `amazonlinux:2` image). A full `goreleaser release
--snapshot --clean` also succeeds end-to-end for both architectures,
producing `.deb`/`.rpm` packages for the rest of the tree; it printed one
warning worth carrying forward -- `artifact already present in the list
name=pam_ssoossh.so` -- because the amd64 and arm64 `.so` outputs share a
bare filename disambiguated only by directory. Nothing in this repo's
nfpm config packages `linux-pam-build` yet, so it is not observed to cause
a problem today, but phase 6 should check it before wiring the `.so` into
a package.

### 6. Add the CI job

A dedicated amd64 job that builds and tests the package. It cannot be folded
into `build-test.yaml` as written, because that job builds with cgo off for
everything else, and the `pam` build tag means `go build ./...` does not
cover the package regardless.

The job runs: `make pam`, `make test-pam`, `make lint-pam`.

**Done:** the `pam` job in `.github/workflows/build-test.yaml`, alongside
the existing `build-test` job. `actionlint` passes against it. Not run in
real CI as part of this work.

### 7. Decide the build-tag question

The old plan says "remove the Phase 1 build-tag exclusion so `go build ./...`
covers it again". **Do not do this without deciding deliberately.**

All six files in `pam_ssoossh/` carry `//go:build pam`. Removing the tag
means every `go build ./...` on every developer machine and every CI job
needs libpam headers and cgo. The tag is what keeps the default build fast,
cgo-free, and cross-platform.

The alternative is to keep the tag and rely on the dedicated job for
coverage, which is what the Makefile already assumes with its separate `pam`,
`test-pam`, and `lint-pam` targets.

Recommendation: **keep the tag.** The exclusion was introduced as a
workaround and has turned out to be the right structure. What must change is
that the tagged targets stop being stubs, which is items 1 and 6. Record the
decision either way so it stops being revisited.

**Decided: kept**, per the recommendation above. `//go:build pam` stays on
all six files in `pam_ssoossh/`; `go build ./...` and `go test ./...` keep
excluding the package everywhere except the dedicated targets and CI job
from items 1 and 6.

## Exit criteria

- `make pam` produces a `.so` in the devcontainer.
- `make test-pam` runs tests rather than skipping.
- `make lint-pam` passes.
- The amd64 CI job runs all three on every pull request.
- A container can be started from inside the devcontainer.
- `goreleaser release --snapshot --clean` builds `linux-pam-build` for amd64
  and arm64.

## Verification

- `ldd --version` inside the chosen build image, recorded in the release
  notes as the supported floor.
- `objdump -T pam_ssoossh.so | grep GLIBC_` on the released artifact, to
  confirm the highest symbol version required matches that floor. This is the
  check that actually catches a mistake here; a successful build in the
  container does not, if the toolchain was newer than the image's libc.
- Load the built module on a target older than the build host and confirm
  `dlopen` succeeds. Until phase 5 the module fails closed, so a successful
  load followed by a clean authentication *failure* is the expected result
  and is exactly what should be tested at this point.

## Constraints

- `pam_ssoossh/` must not import `server/` or `client/`, only `internal/`
  (`.claude/rules/go.md`). Currently respected.
- The module runs in the `auth` group, `sudo` and `su` only.

## What's still open

Confirmed by the 2026-08-15 devcontainer rebuild: `docker-outside-of-docker`
starts a container (item 2), and the arm64 cross build succeeds (item 5's
second half). What's left:

- Run the `pam` job in `.github/workflows/build-test.yaml` in real CI (item
  6) -- this branch has not been pushed, so it has not run there yet.
- Close the gap item 5 records: link `linux-pam-build` against the pinned
  images from item 3 instead of the build host's own glibc, then run this
  phase's Verification section (`ldd --version` in the build image,
  `objdump -T` on the artifact, a load test on an older target) against the
  result. This is real work -- a sysroot from the pinned image or a
  docker-outside-of-docker link step -- not something a rebuild resolves by
  itself.
- Check the `artifact already present in the list name=pam_ssoossh.so`
  goreleaser warning before phase 6 packages the `.so`.
