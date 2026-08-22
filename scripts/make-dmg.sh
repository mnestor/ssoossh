#!/usr/bin/env bash
# Packages a quill-signed-and-notarized darwin binary into a DMG, run as a
# per-build GoReleaser post hook (one invocation per macOS arch, since
# amd64/arm64 ship as separate binaries rather than a lipo-merged universal
# binary). Requires genisoimage and libdmg-hfsplus's `dmg` tool on PATH
# (installed by release.yml before the GoReleaser step) -- there's no native
# hdiutil-based DMG creation available since this runs on a Linux
# self-hosted runner. This mirrors the old, abandoned .gon.json's dmg block
# (output_path/volume_name) now that gon itself is unmaintained.
set -euo pipefail

binary="$1"
arch="$2"
final="$3"

volume_name="pivssh"
staging="dist/dmg-root-${arch}"
raw_image="dist/pivssh-macos-${arch}.img"
output="dist/${final}.dmg"

for cmd in genisoimage dmg; do
  command -v "$cmd" >/dev/null 2>&1 || exit 0
done

rm -rf "$staging" "$raw_image" "$output"
mkdir -p "$staging"
cp "$binary" "$staging/pivssh"

genisoimage -D -V "$volume_name" -no-pad -r -apple -o "$raw_image" "$staging"
dmg "$raw_image" "$output"

rm -rf "$staging" "$raw_image"
