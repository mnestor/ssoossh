#!/bin/sh
# Initialize a SoftHSM2 token and generate a CA key on it, then stage the
# PKCS#11 module where ssoosshd can dlopen it. Idempotent: a token that
# already exists is left completely alone.
#
# Run as a one-shot service that completes before ssoosshd starts. It must
# never run as a long-lived sidecar -- see Dockerfile.hsm-tools.
set -eu

: "${TOKEN_DIR:=/hsm/tokens}"
: "${LIB_DIR:=/hsm/lib}"
: "${PIN_DIR:=/hsm/pin}"
: "${TOKEN_LABEL:=ssoossh-ca}"
: "${KEY_LABEL:=ssoossh-ca}"
: "${KEY_ID:=01}"
: "${KEY_TYPE:=EC:prime384v1}"
: "${STAGE_MODULE:=1}"
# distroless/*-debian12:nonroot runs as 65532; the signer must be able to
# write the token directory (SoftHSM2 takes lock files there even to sign).
: "${RUN_AS_UID:=65532}"
: "${RUN_AS_GID:=65532}"

MODULE=/usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so
PIN_FILE="$PIN_DIR/pin"
SOFTHSM2_CONF="$LIB_DIR/softhsm2.conf"
export SOFTHSM2_CONF

mkdir -p "$TOKEN_DIR" "$LIB_DIR" "$PIN_DIR"

# The conf lives beside the module in LIB_DIR because the signer mounts that
# directory read-only and needs both. tokendir is absolute so the same conf
# works from either container.
cat > "$SOFTHSM2_CONF" <<EOF
directories.tokendir = $TOKEN_DIR
objectstore.backend = file
objectstore.umask = 0077
log.level = ERROR
slots.removable = false
slots.mechanisms = ALL
library.reset_on_fork = false
EOF

# Stage the module itself. Debian 12's libsofthsm2.so resolves every one of
# its dependencies (libcrypto, libstdc++, libgcc_s, libm, libc) against
# distroless/cc-debian12 with no LD_LIBRARY_PATH, which is why hsm.module
# can just name an absolute path in the signer's config.
stage_module() {
	[ "$STAGE_MODULE" = "1" ] || return 0
	cp -f "$MODULE" "$LIB_DIR/libsofthsm2.so"
	chmod 0644 "$LIB_DIR/libsofthsm2.so"
}

# Hand the token, the staged libs and the PIN to the signer's UID. Done last
# so a failure part-way through does not leave a half-owned token.
fix_ownership() {
	chown -R "$RUN_AS_UID:$RUN_AS_GID" "$TOKEN_DIR" "$LIB_DIR" "$PIN_DIR"
	chmod 0700 "$TOKEN_DIR"
	chmod 0400 "$PIN_FILE"
}

# THE GUARD. Compose restarts and Kubernetes init containers re-run this on
# every start; softhsm2-util --init-token on a live token destroys the CA.
# A non-empty token directory means provisioning already happened, and the
# only correct action is to leave it be.
if [ -n "$(ls -A "$TOKEN_DIR" 2>/dev/null || true)" ]; then
	echo "provision: $TOKEN_DIR is not empty, token already provisioned; not touching it."
	stage_module
	fix_ownership
	exit 0
fi

# No PIN yet: generate one rather than shipping a default. A repository that
# carries a known PIN trains people to keep it.
if [ ! -s "$PIN_FILE" ]; then
	od -An -N4 -tu4 /dev/urandom | tr -d ' \n' | cut -c1-8 > "$PIN_FILE"
	echo "provision: generated a random user PIN at $PIN_FILE"
fi
PIN="$(cat "$PIN_FILE")"
SO_PIN="$(od -An -N4 -tu4 /dev/urandom | tr -d ' \n' | cut -c1-8)"

echo "provision: initializing token '$TOKEN_LABEL'"
softhsm2-util --init-token --free \
	--label "$TOKEN_LABEL" \
	--pin "$PIN" \
	--so-pin "$SO_PIN"

echo "provision: generating $KEY_TYPE CA key '$KEY_LABEL' (id $KEY_ID)"
pkcs11-tool --module "$MODULE" \
	--login --pin "$PIN" \
	--keypairgen --key-type "$KEY_TYPE" \
	--label "$KEY_LABEL" --id "$KEY_ID"

# The SO PIN is deliberately not persisted. It only re-initializes or
# unblocks the token, and nothing in this simulation needs to do either;
# keeping it would be one more copy of an authority nobody uses.
stage_module
fix_ownership

echo "provision: done. Objects on the token:"
pkcs11-tool --module "$MODULE" --login --pin "$PIN" --list-objects
