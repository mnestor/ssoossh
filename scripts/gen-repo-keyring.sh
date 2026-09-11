#!/usr/bin/env sh
# Dearmors the published signing key into the binary keyring the
# ssoossh-release deb ships at /usr/share/keyrings.
#
# A build step rather than a tracked binary file: the armoured key in
# gpg-public.asc is the one source, and a committed .gpg alongside it would
# be a second copy to keep in step with no way to notice when it drifted.
#
# Writes to build/, not dist/. goreleaser's before hooks run after --clean
# has emptied dist/ but before it asserts dist/ is empty, so a hook that
# stages anything there fails the run it is part of.
set -eu

src=gpg-public.asc
out=build/ssoossh-archive-keyring.gpg

if [ ! -f "$src" ]; then
	echo "gen-repo-keyring: $src not found" >&2
	exit 1
fi

mkdir -p "$(dirname "$out")"
rm -f "$out"
gpg --batch --yes --dearmor -o "$out" "$src"
echo "gen-repo-keyring: wrote $out"
