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

if [ "$#" -ne 7 ]; then
  echo "usage: $0 <os> <arch> <output-path> <version> <commit> <date> <builtby>" >&2
  exit 1
fi

OS="$1"
ARCH="$2"
OUTPUT_PATH="$3"
VERSION="$4"
COMMIT="$5"
DATE="$6"
BUILTBY="$7"

if [ "$OS" != "linux" ]; then
  echo "build-pam-release-so.sh: no glibc-floor image for $OS/$ARCH, leaving goreleaser's own build in place" >&2
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGES_DIR="$REPO_ROOT/scripts/pam-build-images"
GO_VERSION="$(grep -m1 '^go ' "$REPO_ROOT/go.mod" | awk '{print $2}')"

case "$ARCH" in
  amd64)
    DOCKERFILE="$IMAGES_DIR/centos7-amd64.Dockerfile"
    PLATFORM="linux/amd64"
    ;;
  arm64)
    DOCKERFILE="$IMAGES_DIR/amazonlinux2-arm64.Dockerfile"
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
docker build --platform "$PLATFORM" -f "$DOCKERFILE" --build-arg "GO_VERSION=$GO_VERSION" -t "$IMAGE_TAG" "$IMAGES_DIR" >&2

LDFLAGS="-X github.com/mnestor/ssoossh/internal/version.Version=$VERSION"
LDFLAGS="$LDFLAGS -X github.com/mnestor/ssoossh/internal/version.Commit=$COMMIT"
LDFLAGS="$LDFLAGS -X github.com/mnestor/ssoossh/internal/version.Date=$DATE"
LDFLAGS="$LDFLAGS -X github.com/mnestor/ssoossh/internal/version.BuiltBy=$BUILTBY"

mkdir -p "$(dirname "$OUTPUT_PATH")"

# No bind mount for the source tree: this script runs inside the
# devcontainer under docker-outside-of-docker, where the daemon is the real
# host's and its filesystem paths do not match the devcontainer's
# (docs/docker-setup.md -- /workspace/ssoossh here is
# /home/mnestor/git/ssoossh there). A -v bind mount is resolved daemon-side
# and would silently mount the wrong (or no) directory. `docker exec`/`cp`
# instead stream through the API and are resolved client-side, so they work
# the same whether the daemon is local (a plain CI runner) or remote
# (docker-outside-of-docker) without needing to know which.
CONTAINER_ID="$(docker run -d --platform "$PLATFORM" \
  -v "ssoossh-pam-gomodcache-$ARCH:/go/pkg/mod" \
  -e CGO_ENABLED=1 \
  -e CC=gcc \
  -e GOOS=linux \
  -e GOARCH="$ARCH" \
  "$IMAGE_TAG" \
  sleep 600)"
trap 'docker rm -f "$CONTAINER_ID" >/dev/null 2>&1 || true' EXIT

docker exec "$CONTAINER_ID" mkdir -p /workspace
tar --exclude='.git' --exclude='dist' --exclude='.build' \
    --exclude='frontend/node_modules' --exclude='frontend/dist' \
    -C "$REPO_ROOT" -cf - . \
  | docker exec -i "$CONTAINER_ID" tar -xf - -C /workspace >&2

docker exec -w /workspace "$CONTAINER_ID" \
  go build -tags=pam,nomsgpack -buildmode=c-shared -ldflags "$LDFLAGS" -o /workspace/pam_ssoossh.so ./pam_ssoossh/ >&2

docker cp "$CONTAINER_ID:/workspace/pam_ssoossh.so" "$OUTPUT_PATH" >&2

echo "build-pam-release-so.sh: wrote glibc-floor-linked $OUTPUT_PATH ($ARCH)" >&2
