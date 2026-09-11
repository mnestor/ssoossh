#!/usr/bin/env bash
# Uploads a repository tree built by gen-repo.sh to S3-compatible object
# storage (Cloudflare R2).
#
# Upload order is the whole design. A package manager reads the metadata
# first and then fetches what the metadata names, so metadata must never
# describe a file that is not there yet. Packages go up first and metadata
# last; a client that catches the window in between sees the old, complete
# repository rather than a new, broken one.
#
# Nothing is ever deleted. The reverse hazard -- dropping a package while
# metadata still names it -- is avoided by never doing it here at all.
# Pruning old releases is a separate, deliberate operation, and it has to
# remove the metadata reference first and the package afterwards, which is
# the opposite order to this script. A single `rclone sync --delete` pass
# does both wrongly at once, which is why this uses copy and not sync.
#
# Cache lifetimes differ by kind, and getting this wrong is the classic way
# a repository that tested fine starts failing a week later:
#
#   packages   immutable. A given NEVRA is that file forever, so it can be
#              cached indefinitely.
#   metadata   mutable at a fixed URL. repomd.xml, repodata/, InRelease,
#              Release, Packages: all are replaced in place every publish.
#              Cached, a client pairs fresh metadata with a stale index and
#              fails on a checksum mismatch.
#
# Usage:
#   scripts/publish-repo.sh --repo repo --bucket <name> --prefix <path> [--dry-run]
#
# Credentials come from the environment, never arguments:
#   R2_KEY_ID, R2_ACCESS_KEY, R2_S3_ENDPOINT
set -euo pipefail

repo=repo
bucket=""
# No default. "ssoossh" is the live path, and a default that publishes live
# is the wrong way round: a caller who forgets the flag, or passes a value
# that came back empty from somewhere, should be stopped rather than quietly
# sent to the location every installed host reads.
prefix=""
dry=""

while [ $# -gt 0 ]; do
	case "$1" in
	--repo) repo=$2; shift 2 ;;
	--bucket) bucket=$2; shift 2 ;;
	--prefix) prefix=$2; shift 2 ;;
	--dry-run) dry="--dry-run"; shift ;;
	-h | --help) sed -n '2,35p' "$0"; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

[ -n "$bucket" ] || { echo "publish-repo: --bucket is required" >&2; exit 2; }
[ -n "$prefix" ] || { echo "publish-repo: --prefix is required (it has no default; 'ssoossh' is live)" >&2; exit 2; }
[ -d "$repo" ] || { echo "publish-repo: no such directory: $repo" >&2; exit 2; }

for var in R2_KEY_ID R2_ACCESS_KEY R2_S3_ENDPOINT; do
	eval "v=\${$var:-}"
	[ -n "$v" ] || { echo "publish-repo: $var is not set" >&2; exit 2; }
done

# Refuse to publish a tree nobody signed.
#
# gen-repo.sh builds unsigned when no --key-id is given, which is right for a
# test run and catastrophic for a real one: an unsigned repository fails
# verification, and the thing a user reaches for when a repository fails
# verification is the flag that turns verification off. Publishing one would
# train exactly the habit this whole effort exists to end.
#
# It is checked here rather than there because this is the step that does the
# harm, and because an empty --key-id is easy to produce by accident -- a
# shell substitution that returned nothing hands gen-repo.sh an empty string,
# which it accepts with a warning nobody reads in a CI log.
missing=""
[ ! -d "$repo/apt/dists" ] || [ -f "$repo/apt/dists/stable/InRelease" ] || missing="$missing apt/dists/stable/InRelease"
for rv_dir in "$repo"/yum/el/*/; do
	[ -d "$rv_dir" ] || continue
	[ -f "$rv_dir/repodata/repomd.xml.asc" ] || missing="$missing ${rv_dir}repodata/repomd.xml.asc"
done

if [ -n "$missing" ]; then
	echo "publish-repo: refusing to publish an unsigned repository" >&2
	echo "publish-repo: missing signatures:" >&2
	for m in $missing; do echo "publish-repo:   $m" >&2; done
	echo "publish-repo: re-run gen-repo.sh with --key-id; an empty value" >&2
	echo "publish-repo: produces an unsigned tree and only warns." >&2
	exit 1
fi

command -v rclone >/dev/null 2>&1 || { echo "publish-repo: rclone is not installed" >&2; exit 1; }

# Configured entirely through the environment so no file holding the secret
# is ever written. rclone builds a remote from RCLONE_CONFIG_<NAME>_<KEY>,
# and the _TYPE variable is what makes the remote exist at all.
export RCLONE_CONFIG_R2_TYPE=s3
export RCLONE_CONFIG_R2_PROVIDER=Cloudflare
export RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_KEY_ID"
export RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_ACCESS_KEY"
export RCLONE_CONFIG_R2_ENDPOINT="$R2_S3_ENDPOINT"
export RCLONE_CONFIG_R2_REGION=auto

dest="R2:$bucket/$prefix"

# A year, and immutable: a package at a given NEVRA never changes, so a
# cached copy can never be wrong.
immutable="Cache-Control: public, max-age=31536000, immutable"
# Metadata is replaced in place on every publish and must never be served
# from a cache.
mutable="Cache-Control: no-cache, must-revalidate"

upload() {
	local src=$1 dst=$2 cache=$3 what=$4

	if [ ! -e "$src" ]; then
		return 0
	fi
	echo "publish-repo: $what"
	rclone copy $dry --checksum --header-upload "$cache" "$src" "$dst"
}

# --- 1. packages, immutable -------------------------------------------------
# Everything a metadata file could possibly name has to exist before any
# metadata does.
upload "$repo/yum/pool"      "$dest/yum/pool"      "$immutable" "yum packages"
upload "$repo/apt/pool"      "$dest/apt/pool"      "$immutable" "apt packages"

# --- 2. the signing key -----------------------------------------------------
# Not metadata, but a client fetches it before it can verify anything, so it
# belongs on the early side. Short cache: a key rotation must be able to
# take effect.
upload "$repo/gpg-public.asc" "$dest/gpg-public.asc" "$mutable" "signing key"

# --- 3. metadata, last and uncached ----------------------------------------
upload "$repo/yum/el"        "$dest/yum/el"        "$mutable"   "yum metadata"
upload "$repo/apt/dists"     "$dest/apt/dists"     "$mutable"   "apt metadata"

if [ -n "$dry" ]; then
	echo "publish-repo: DRY RUN -- nothing was uploaded"
else
	echo "publish-repo: published to $dest"
fi
