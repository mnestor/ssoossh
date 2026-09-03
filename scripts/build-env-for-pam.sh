#!/usr/bin/env bash
#
# Install what `make pam`, `make test-pam`, and .goreleaser.yml's
# linux-pam-build-amd64/-arm64 need: libpam headers for the host's own arch,
# plus the matching foreign-arch libpam0g-dev (and, via its own package
# dependency, the runtime libpam0g.so it links against) for the arm64
# cross target. Not needed inside the devcontainer -- .devcontainer/Dockerfile
# already installs all of this into the image. This script exists for bare
# hosts that build the package outside that image: GitHub-hosted CI runners
# (.github/workflows/build.yaml's build-most job runs the equivalent install
# in Dockerfile.runner instead) and an amd64 dev VM/host checkout.
#
# No cross gcc / cross libc package needed for the arm64 target: unlike a
# native cross-gcc build, which links against whatever glibc the *build
# host* has, .goreleaser.yml's linux-pam-build-* entries cross-compile with
# zig cc, which pins the actual linked-against glibc floor itself (2.17
# amd64, 2.26 arm64 -- see that file). This script's job is only getting a
# real, dynamically-linkable libpam.so.0 onto the build host for each target
# arch; a headers-only install makes the arm64 build silently fall back to
# statically linking libpam.a instead, which is why libpam0g-dev:arm64 (not
# just its headers) is the package installed below.
#
# Usage: scripts/build-env-for-pam.sh

set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  SUDO="sudo"
fi

if command -v apt-get >/dev/null 2>&1; then
  # arm64 is a foreign architecture on the amd64 runners this targets; it
  # must be registered before any :arm64 package resolves.
  $SUDO dpkg --add-architecture arm64
  $SUDO apt-get update
  $SUDO apt-get install -y \
    libpam0g-dev \
    libpam0g-dev:arm64
elif command -v dnf >/dev/null 2>&1; then
  $SUDO dnf install -y pam-devel gcc make
elif command -v yum >/dev/null 2>&1; then
  $SUDO yum install -y pam-devel gcc make
else
  echo "unsupported package manager: install libpam headers and a cgo toolchain manually" >&2
  exit 1
fi

# Only needed for a local linux-pam-build-* build (zig cc, not `make pam`'s
# native gcc): an isolated copy of just the PAM headers for CGO_CFLAGS to
# point at, matching Dockerfile.runner. security/*.h is arch-independent, so
# one copy covers both targets. Deliberately not /usr/include itself -- zig
# cc ships its own versioned glibc headers for the pinned target floor, and
# the real /usr/include would pull the host's glibc headers in alongside
# them and collide (__GLIBC_MINOR__ redefined, etc).
if [ -d /usr/include/security ]; then
  $SUDO mkdir -p /opt/pam-include/security
  $SUDO cp /usr/include/security/*.h /opt/pam-include/security/
fi

echo
echo "Done. Verify with:"
echo "  make pam"
