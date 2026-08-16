#!/usr/bin/env bash
#
# Install what `make pam`, `make test-pam`, and `goreleaser build --id
# linux-pam-build` need: libpam headers plus, for the arm64 cross target,
# the arm64 libc headers and cross gcc. Not needed inside the devcontainer
# -- .devcontainer/Dockerfile already installs all of this into the image.
# This script exists for bare hosts that build the package outside that
# image: GitHub-hosted CI runners (.github/workflows/build-test.yaml's
# pam job, build-release.yaml) and an amd64 dev VM/host checkout.
#
# See docs/release-phase3-pam-build-env.md.
#
# Does NOT get pam_ssoossh to an old-glibc-safe binary: cross-arch packages
# change target CPU, not glibc version, so a build using what this script
# installs still links against the host's own glibc. That gap is recorded
# in .goreleaser.yml next to `linux-pam-build` and is not this script's job
# to close.
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
    libpam0g-dev:arm64 \
    libc6-dev-arm64-cross \
    gcc-aarch64-linux-gnu
elif command -v dnf >/dev/null 2>&1; then
  $SUDO dnf install -y pam-devel gcc make
elif command -v yum >/dev/null 2>&1; then
  $SUDO yum install -y pam-devel gcc make
else
  echo "unsupported package manager: install libpam headers and a cgo toolchain manually" >&2
  exit 1
fi

echo
echo "Done. Verify with:"
echo "  make pam"
