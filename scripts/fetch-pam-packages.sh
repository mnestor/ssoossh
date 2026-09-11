#!/usr/bin/env bash
# Fetches ssoossh-pam's Linux packages into the tree gen-repo.sh indexes.
#
# The PAM module is built and released from its own repository, but it has
# to be served from this one's repository or it is not served at all. That
# matters more for it than for anything built here: Debian ships no-debsig
# uncommented in dpkg.cfg and does not install debsig-verify, so the
# signature nfpm writes into a .deb is inert for essentially every user.
# For apt, the repository's own signed metadata is the only integrity check
# that actually runs, and a .deb downloaded from a release page has none.
#
# One writer, deliberately. This repository's publish is a single ordered
# sequence -- packages before metadata, nothing ever deleted, split cache
# lifetimes -- and those hold only while one job is doing it. Two projects
# regenerating metadata over one shared pool would race in a way the pool's
# own duplicate check cannot see, because that check compares what is in
# front of it during one run and knows nothing about a second run happening
# elsewhere. So the packages come here and are published from here.
#
# Everything fetched is verified before it is filed, against the signed
# manifest the release publishes rather than against the packages' own
# signatures. That is not a preference: rpm would check its packages at
# install time, but nothing downstream checks a .deb, so the check has to
# happen here or nowhere. The signature is verified in a throwaway keyring
# holding only the published key, so what is checked is what a client would
# check with, not whatever the CI keyring has accumulated.
#
# Usage:
#   scripts/fetch-pam-packages.sh [--tag vX.Y.Z] [--out DIR] [--key FILE]
set -euo pipefail

repo=mnestor/ssoossh-pam
tag=""
# Under dist/ so gen-repo.sh finds these -- it enumerates packages with
# find, which descends -- while the build steps that glob dist/*.pkg and
# dist/*.msi for the installers do not see them.
out=dist/external
key=gpg-public.asc

while [ $# -gt 0 ]; do
	case "$1" in
	--repo) repo=$2; shift 2 ;;
	--tag) tag=$2; shift 2 ;;
	--out) out=$2; shift 2 ;;
	--key) key=$2; shift 2 ;;
	-h | --help) sed -n '2,28p' "$0"; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

[ -f "$key" ] || { echo "fetch-pam: no such key file: $key" >&2; exit 2; }

command -v gh >/dev/null 2>&1 || { echo "fetch-pam: gh is not installed" >&2; exit 1; }
command -v gpg >/dev/null 2>&1 || { echo "fetch-pam: gpg is not installed" >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "fetch-pam: sha256sum is not installed" >&2; exit 1; }

staging=$(mktemp -d)
keyring=$(mktemp -d)
chmod 700 "$keyring"
trap 'rm -rf "$staging" "$keyring"' EXIT

# Empty --tag means the latest release, which is what gh does with no tag.
if [ -n "$tag" ]; then
	set -- "$tag" --repo "$repo"
else
	set -- --repo "$repo"
fi

echo "fetch-pam: downloading from $repo ${tag:-(latest)}"
gh release download "$@" \
	--dir "$staging" \
	--pattern 'SHA256SUMS' \
	--pattern 'SHA256SUMS.asc' \
	--pattern '*.rpm' \
	--pattern '*.deb'

[ -f "$staging/SHA256SUMS" ] || { echo "fetch-pam: the release publishes no SHA256SUMS" >&2; exit 1; }
[ -f "$staging/SHA256SUMS.asc" ] || { echo "fetch-pam: the release publishes no SHA256SUMS.asc" >&2; exit 1; }

# The manifest's signature, checked against the published key alone.
if ! GNUPGHOME="$keyring" gpg --batch --quiet --import "$key" 2>/dev/null; then
	echo "fetch-pam: could not import $key" >&2
	exit 1
fi
if ! GNUPGHOME="$keyring" gpg --batch --verify "$staging/SHA256SUMS.asc" "$staging/SHA256SUMS" 2>/dev/null; then
	echo "fetch-pam: SHA256SUMS is not signed by the key in $key" >&2
	echo "fetch-pam: refusing to file packages this repository would then sign metadata over." >&2
	exit 1
fi
echo "fetch-pam: manifest signature verified against $key"

# Every package fetched must be named in the manifest. Checking only the
# manifest's own entries would let an asset that is not listed through, and
# an unlisted file is exactly the one worth stopping.
cd "$staging"
packages=$(find . -maxdepth 1 \( -name '*.rpm' -o -name '*.deb' \) -type f | sed 's|^\./||' | sort)
[ -n "$packages" ] || { echo "fetch-pam: the release has no .rpm or .deb assets" >&2; exit 1; }

unlisted=""
for p in $packages; do
	grep -q "[ *]${p}\$" SHA256SUMS || unlisted="$unlisted $p"
done
if [ -n "$unlisted" ]; then
	echo "fetch-pam: these packages are not named in the signed manifest:" >&2
	for u in $unlisted; do echo "fetch-pam:   $u" >&2; done
	exit 1
fi

# And every one of them must match the digest the manifest gives it.
for p in $packages; do
	grep "[ *]${p}\$" SHA256SUMS
done > .expected
if ! sha256sum --check --quiet .expected; then
	echo "fetch-pam: a package does not match its digest in the signed manifest" >&2
	exit 1
fi
rm -f .expected
cd - >/dev/null

mkdir -p "$out"
n=0
for p in $packages; do
	cp "$staging/$p" "$out/$p"
	n=$((n + 1))
done

echo "fetch-pam: verified and filed $n package(s) into $out"
for p in $packages; do echo "fetch-pam:   $p"; done
