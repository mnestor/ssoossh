#!/usr/bin/env bash
set -euo pipefail

# Links pam_ssoossh.so against the glibc floor pinned in .goreleaser.yml
# (centos:7 for amd64, glibc 2.17; amazonlinux:2 for arm64, glibc 2.26) by
# running the actual go build inside that distribution's own container,
# rather than cross-linking from the build host's glibc. goreleaser's own
# build of linux-pam-build (CC=x86_64-linux-gnu-gcc / aarch64-linux-gnu-gcc)
# links against whatever glibc the build host has -- see the comment above
# linux-pam-build in .goreleaser.yml. This script overwrites that output
# with the correctly-linked artifact via a post-build hook.
#
# See docs/release-phase3-pam-build-env.md (why the floor matters, why the
# pinned images were chosen) and docs/release-phase6-artifacts.md (why
# phase 6 owns closing this gap).
#
# Usage: build-pam-release-so.sh <os> <arch> <output-path> <version> <commit> <date> <builtby>
# Invoked from linux-pam-build's hooks.post in .goreleaser.yml; not meant to
# be run by hand except to debug that hook.

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <arch> <action> <snapshot>" >&2
  exit 1
fi

ARCH="${1}"
ACTION="${2}"
SNAPSHOT="${3}"
UID=$(id -u)
GID=$(id -g)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGES_DIR="${REPO_ROOT}/scripts/pam-build-images"
GORELEASER_VERSION="$(curl -s https://api.github.com/repos/goreleaser/goreleaser/releases/latest |jq -r '.tag_name')"
GO_VERSION="$(grep -m1 '^go ' "$REPO_ROOT/go.mod" | awk '{print $2}')"

case "$ARCH" in
  amd64)
    DOCKERFILE="${IMAGES_DIR}/centos7-amd64.Dockerfile"
    PLATFORM="linux/amd64"
    ;;
  arm64)
    DOCKERFILE="${IMAGES_DIR}/amazonlinux2-arm64.Dockerfile"
    PLATFORM="linux/arm64"
    # Emulated aarch64 execution on an amd64 build host needs the qemu
    # binfmt handler registered against the (shared) host kernel. Cheap and
    # idempotent, so just do it rather than trying to detect whether it's
    # already there -- /proc/sys/fs/binfmt_misc isn't reliably visible from
    # inside a nested devcontainer even when the host kernel has it.
    docker run --rm --privileged tonistiigi/binfmt --install arm64 >&2
    ;;
  *)
    echo "build-pam-release-so.sh: no pinned glibc floor image for linux/$ARCH" >&2
    exit 1
    ;;
esac

IMAGE_TAG="ssoossh-pam-build:$ARCH"
docker build --platform "${PLATFORM}" -f "${DOCKERFILE}" \
  --build-arg "GO_VERSION=${GO_VERSION}" \
  --build-arg "GORELEASER_VERSION=${GORELEASER_VERSION}" \
  --build-arg "UID=${UID}" \
  --build-arg "GID=${GID}" \
  -t "${IMAGE_TAG}" "${IMAGES_DIR}"

docker run --rm --platform "${PLATFORM}" \
  -v "${REPO_ROOT}:/workspace" \
  -v "ssoossh-pam-gomodcache-${ARCH}:/go/pkg/mod" "${IMAGE_TAG}" "${ARCH}" "${ACTION}" "${SNAPSHOT}"

