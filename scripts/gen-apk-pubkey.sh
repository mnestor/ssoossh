#!/usr/bin/env sh
# Extracts the public half of the apk signing key into dist/, where the
# release picks it up as a published artifact.
#
# A host installing the .apk by direct download verifies the signature
# against /etc/apk/keys/ssoossh.rsa.pub and has nowhere else to get that
# file, so it has to ship with the release rather than live only wherever
# the private key is kept.
#
# Runs from goreleaser's before hooks for the same reason the keyring
# generator does: --clean empties dist/ immediately beforehand, so anything
# staged there by an earlier workflow step would already be gone.
#
# Absent key is not an error. A local snapshot build has no apk.pem and
# should not need one; the release job fails on the signing step instead,
# which is a clearer place to learn the key is missing than here.
set -eu

src=apk.pem
out=dist/ssoossh.rsa.pub

if [ ! -f "$src" ]; then
	echo "gen-apk-pubkey: $src not present, skipping (snapshot build)" >&2
	exit 0
fi

mkdir -p "$(dirname "$out")"
openssl rsa -in "$src" -pubout -out "$out"
echo "gen-apk-pubkey: wrote $out"
