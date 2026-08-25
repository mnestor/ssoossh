#!/usr/bin/env bash
#
# Run the frontend semgrep scan (the merge gate) so that it works from any
# worktree, not just the directory the devcontainer was started in.
#
# Why not a plain bind mount. Inside a devcontainer talking to the HOST's
# docker daemon, `-v $PWD:/src` resolves $PWD on the host, not in here. Only
# the path the devcontainer was started from round-trips; a sibling git
# worktree does not exist as far as the host daemon is concerned, so the
# mount comes up EMPTY and semgrep fails with the thoroughly unhelpful
# "Invalid scanning root: frontend/src".
#
# So the sources travel in by `docker cp` instead -- the same trick, for the
# same reason, as test/e2e/harness/nats.go's certificate handling.
#
# Usage: scripts/semgrep.sh [scan-root...]   (default: frontend/src)
set -euo pipefail

# Keep this pin in step with the semgrep job's `image:` in
# .github/workflows/security.yaml -- this target exists to reproduce that job
# exactly, and two different pins would make a local pass meaningless.
IMAGE="${SEMGREP_IMAGE:-semgrep/semgrep:1.174.0}"
if [[ $# -gt 0 ]]; then
    ROOTS=("$@")
else
    ROOTS=(frontend/src)
fi

command -v docker >/dev/null 2>&1 || { echo "semgrep: docker is not installed" >&2; exit 1; }

# ~/.docker/config.json in this devcontainer names a VS Code credential
# helper that exits 255 from a plain terminal, breaking even anonymous
# pulls. A throwaway empty config sidesteps it without touching the real one.
own_docker_config=""
if [[ -z "${DOCKER_CONFIG:-}" ]] && grep -q credsStore "${HOME}/.docker/config.json" 2>/dev/null; then
    DOCKER_CONFIG="$(mktemp -d)"
    printf '{}' >"${DOCKER_CONFIG}/config.json"
    export DOCKER_CONFIG
    own_docker_config="$DOCKER_CONFIG"
fi

docker info >/dev/null 2>&1 || { echo "semgrep: docker daemon unavailable" >&2; exit 1; }

for root in "${ROOTS[@]}"; do
    [[ -e "$root" ]] || { echo "semgrep: scan root '$root' does not exist in $(pwd)" >&2; exit 1; }
done

# Stage the roots under one directory, preserving their relative paths, so a
# single docker cp reproduces the layout the scan expects.
stage="$(mktemp -d)"
# Plain ifs, not && chains: under `set -e` a false test in an `A && B` list
# makes the list fail, which aborts the trap before the later steps run.
# Only a DOCKER_CONFIG this script created is removed -- never one the
# caller supplied.
cleanup() {
    rm -rf "$stage"
    if [[ -n "${container:-}" ]]; then
        docker rm -f "$container" >/dev/null 2>&1 || true
    fi
    if [[ -n "$own_docker_config" ]]; then
        rm -rf "$own_docker_config"
    fi
    return 0
}
trap cleanup EXIT

for root in "${ROOTS[@]}"; do
    mkdir -p "$stage/$(dirname "$root")"
    cp -a "$root" "$stage/$(dirname "$root")/"
done

# .semgrepignore has to travel too, or the scan silently widens: the ignore
# list excludes generated wire types and build output, and without it those
# come back as findings CI would never report. A scan that does not match
# CI's scope is not the merge gate, however green it looks.
if [[ -f .semgrepignore ]]; then
    cp -a .semgrepignore "$stage/"
fi

# Make the staging tree a git repository. semgrep only honours
# .semgrepignore, and only limits itself to tracked files, when it can find
# a project root -- which it detects by looking for .git. Without this the
# staged copy scans 53 files where CI scans 50, silently including the three
# generated wire-type files .semgrepignore exists to exclude. `git add` is
# enough; no commit and no identity are needed.
if command -v git >/dev/null 2>&1; then
    git -C "$stage" init -q 2>/dev/null &&
        git -C "$stage" add -A 2>/dev/null || true
fi

container="$(docker create -w /src "$IMAGE" \
    semgrep scan --error --metrics=off \
    --config p/typescript --config p/javascript "${ROOTS[@]}")"

# Trailing /. copies the CONTENTS of the staging directory into /src.
# Without it docker nests the staging directory itself inside /src, since
# `docker create -w /src` has already created /src.
docker cp "$stage/." "$container:/src" >/dev/null

docker start -a "$container"
