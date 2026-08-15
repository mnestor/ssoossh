# Phase 3: PAM build environment

**Status: planned.** Part of [release-plan.md](release-plan.md).

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

### 4. Rewrite `scripts/build-env-for-pam.sh`

Currently malformed: loose package-name fragments, a stray `yum install`
pasted outside any command, and `dpkg --add-architecture x86-64`, which is
invalid, with the following line already doing `amd64` correctly.

It is untracked, so this is closer to writing it than fixing it. Decide what
it is for first. If the container images carry their own toolchain, a script
that installs cross-compilers on the host may not need to exist at all.

### 5. Re-enable the goreleaser build

`.goreleaser.yml:31-32` has `id: linux-pam-build` with `skip: true`. The rest
of the block is already correct: `buildmode: c-shared`, `dir: ./pam_ssoossh/`,
`flags: [-tags=nomsgpack,pam]`, `CGO_ENABLED=1`, linux amd64 and arm64, and
per-arch `CC` overrides (`x86_64-linux-gnu-gcc`, `aarch64-linux-gnu-gcc`)
with matching `PKG_CONFIG_PATH`.

Remove `skip: true` and confirm the cross settings work. Note that phase 6
owns whether the `.so` reaches the nfpm packages; this item is only about it
building.

### 6. Add the CI job

A dedicated amd64 job that builds and tests the package. It cannot be folded
into `build-test.yaml` as written, because that job builds with cgo off for
everything else, and the `pam` build tag means `go build ./...` does not
cover the package regardless.

The job runs: `make pam`, `make test-pam`, `make lint-pam`.

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
