#!/usr/bin/env bash
# Guards the invariant that the Go toolchain CI runs on is new enough for
# the module it is scanning:
#
#   1. Both images pin the same GO_VERSION. Dockerfile.devcontainer and
#      Dockerfile.runner are built from the same commit and are meant to be
#      the same environment; a developer whose devcontainer builds cleanly
#      should not be handed a CI failure that only reproduces in the runner.
#   2. That pin is at least go.mod's `go` directive. This is the one that
#      reached CI: go.mod moved to 1.27 and Dockerfile.devcontainer moved
#      with it, but Dockerfile.runner stayed on go1.26.6 without a single
#      job going red.
#   3. The toolchain running right now is at least go.mod's `go` directive.
#      Check 2 reads the Dockerfiles; this one reads reality, so it also
#      catches the window where the pin is correct but the published image
#      has not been rebuilt from it yet.
#
# Why this needs its own check rather than falling out of a build: every
# ordinary Go step survives an image that is too old, because GOTOOLCHAIN=auto
# silently downloads a newer toolchain and re-execs. The tools that cannot do
# that are the prebuilt binaries baked into the image -- govulncheck above all,
# which type-checks the module in-process with the go/types it was compiled
# against. An image behind go.mod makes it fail every package in the module
# with "requires newer Go version", and because that scan is advisory it
# reported the wall of errors as if it were a clean result.
#
# Run from the repo root: scripts/check-go-version.sh (or `make check-go-version`).
set -euo pipefail

readonly RUNNER=.github/docker/Dockerfile.runner
readonly DEVCONTAINER=.github/docker/Dockerfile.devcontainer

status=0

# True when $1 <= $2 under version ordering. `sort -V` orders 1.27 below
# 1.27.0, which is what we want: go.mod's "1.27" is a floor that any 1.27.x
# toolchain satisfies.
version_le() {
	[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$1" ]
}

arg_version() {
	awk -F= '/^ARG[ \t]+GO_VERSION=/ { print $2; exit }' "$1"
}

# The `go` directive, e.g. "1.27". This is the language floor, and the thing
# a too-old toolchain refuses to type-check.
required=$(awk '/^go[ \t]+[0-9]/ { print $2; exit }' go.mod)
if [ -z "$required" ]; then
	echo "could not read the 'go' directive from go.mod" >&2
	exit 1
fi

# A `toolchain` directive, if one is ever added, raises the floor above the
# `go` line. Absent today; read rather than assumed so that adding one does
# not quietly leave this check measuring the wrong thing.
toolchain_directive=$(awk '/^toolchain[ \t]+go[0-9]/ { sub(/^go/, "", $2); print $2; exit }' go.mod)
if [ -n "$toolchain_directive" ] && version_le "$required" "$toolchain_directive"; then
	required=$toolchain_directive
fi

runner_pin=$(arg_version "$RUNNER")
devcontainer_pin=$(arg_version "$DEVCONTAINER")

for file in "$RUNNER" "$DEVCONTAINER"; do
	if [ -z "$(arg_version "$file")" ]; then
		echo "$file has no 'ARG GO_VERSION=' line."
		echo
		echo "The Go version has to be readable from the Dockerfile for this check to"
		echo "mean anything. Pin it as an ARG rather than inlining it into the"
		echo "download URL."
		echo
		status=1
	fi
done

# Nothing below can say anything useful without both pins.
if [ "$status" -ne 0 ]; then
	exit "$status"
fi

if [ "$runner_pin" != "$devcontainer_pin" ]; then
	echo "The two images pin different Go versions:"
	echo "  $RUNNER:       $runner_pin"
	echo "  $DEVCONTAINER: $devcontainer_pin"
	echo
	echo "They are built from the same commit and are meant to be the same"
	echo "environment. Move them together."
	echo
	status=1
fi

for pair in "$RUNNER:$runner_pin" "$DEVCONTAINER:$devcontainer_pin"; do
	file=${pair%%:*}
	pin=${pair#*:}
	if ! version_le "$required" "$pin"; then
		echo "$file pins Go $pin, but go.mod requires $required."
		echo
		echo "GOTOOLCHAIN=auto hides this from every ordinary Go step by downloading"
		echo "a newer toolchain, but the prebuilt binaries baked into the image --"
		echo "govulncheck especially -- cannot switch, and fail every package instead."
		echo
		echo "Bump ARG GO_VERSION in both Dockerfiles under .github/docker/."
		echo
		status=1
	fi
done

# What is actually running, not what the Dockerfile claims. GOTOOLCHAIN=local
# pins this to the baked toolchain so an on-demand download cannot mask a
# stale image, and it runs outside the module so an unsatisfiable requirement
# is reported here rather than erroring out of the subshell.
running=$(cd / && GOTOOLCHAIN=local go env GOVERSION 2>/dev/null | sed 's/^go//')
if [ -z "$running" ]; then
	echo "could not determine the running Go toolchain version" >&2
	exit 1
fi

if ! version_le "$required" "$running"; then
	echo "The Go toolchain running this check is $running, but go.mod requires $required."
	echo
	if [ "$runner_pin" = "$devcontainer_pin" ] && version_le "$required" "$runner_pin"; then
		echo "The Dockerfiles already pin $runner_pin, so this is a stale image rather"
		echo "than a stale pin: .github/workflows/build-image.yaml republishes the"
		echo "images on push to main. Wait for that run to finish, or rebuild the"
		echo "devcontainer locally."
	else
		echo "Bump ARG GO_VERSION in both Dockerfiles under .github/docker/, then let"
		echo "build-image.yaml republish the images."
	fi
	echo
	status=1
fi

if [ "$status" -eq 0 ]; then
	echo "go version: go.mod requires $required, both images pin $runner_pin, running $running"
fi
exit "$status"
