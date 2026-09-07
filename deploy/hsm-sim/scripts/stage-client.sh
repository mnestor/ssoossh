#!/bin/sh
# Stage p11-kit's PKCS#11 client module where ssoosshd can dlopen it. Used
# only by the network simulation: the signer loads p11-kit-client.so instead
# of a real provider, and that module forwards every PKCS#11 call over a
# socket to the hsm service.
#
# Unlike libsofthsm2.so, p11-kit-client.so needs libffi.so.8, which
# distroless/cc-debian12 does not ship. Until the server image carries it,
# the signer needs LD_LIBRARY_PATH pointing at LIB_DIR -- which is why that
# directory is mounted read-only there. See
# ../../docs/proposals/hsm-cloud-readiness.md, "Tier 1".
set -eu

: "${LIB_DIR:=/hsm/lib}"
: "${RUN_AS_UID:=65532}"
: "${RUN_AS_GID:=65532}"

CLIENT=/usr/lib/x86_64-linux-gnu/pkcs11/p11-kit-client.so

mkdir -p "$LIB_DIR"
cp -f "$CLIENT" "$LIB_DIR/p11-kit-client.so"

# Copy libffi by following the symlink to the real file, then recreate the
# soname the loader actually searches for.
FFI="$(find /usr/lib /lib -name 'libffi.so.8*' -type f 2>/dev/null | head -1)"
if [ -z "$FFI" ]; then
	echo "stage-client: could not find libffi.so.8" >&2
	exit 1
fi
cp -f "$FFI" "$LIB_DIR/$(basename "$FFI")"
[ "$(basename "$FFI")" = "libffi.so.8" ] || ln -sf "$(basename "$FFI")" "$LIB_DIR/libffi.so.8"

chmod 0644 "$LIB_DIR"/p11-kit-client.so "$LIB_DIR"/libffi.so.8*
chown -R "$RUN_AS_UID:$RUN_AS_GID" "$LIB_DIR"

echo "stage-client: staged into $LIB_DIR:"
ls -la "$LIB_DIR"
