#!/bin/sh
# Client half of the simulated network HSM link. Presents a local Unix
# socket that p11-kit-client.so inside the signer connects to, and forwards
# it over mTLS to the hsm service.
#
# This sidecar exists because the signer image is distroless: it has no
# shell and no socat, and it should not gain either. The socket file lands
# on a volume shared with the signer, which is the only thing the two
# containers exchange.
set -eu

: "${HSM_HOST:=hsm}"
: "${HSM_PORT:=12345}"
: "${CERT_DIR:=/certs}"
: "${SOCKET_PATH:=/run/p11/p11.sock}"
: "${RUN_AS_UID:=65532}"
: "${RUN_AS_GID:=65532}"

mkdir -p "$(dirname "$SOCKET_PATH")"
rm -f "$SOCKET_PATH"

# Wait for the appliance before advertising a socket, so the signer's
# startup failure (if any) names the HSM rather than a missing file.
i=0
until socat -T2 /dev/null \
	"OPENSSL:$HSM_HOST:$HSM_PORT,cert=$CERT_DIR/client.pem,cafile=$CERT_DIR/ca.pem,verify=1,commonname=$HSM_HOST" \
	2>/dev/null; do
	i=$((i + 1))
	[ "$i" -gt 60 ] && { echo "hsm-bridge: $HSM_HOST:$HSM_PORT never came up" >&2; exit 1; }
	sleep 1
done
echo "hsm-bridge: $HSM_HOST:$HSM_PORT is accepting connections"

# commonname pins the server certificate's identity; without it verify=1
# would accept any certificate the simulation CA signed, including the
# client's own.
( sleep 1
  chown "$RUN_AS_UID:$RUN_AS_GID" "$SOCKET_PATH" 2>/dev/null || true
  chmod 0600 "$SOCKET_PATH" 2>/dev/null || true ) &

echo "hsm-bridge: presenting $SOCKET_PATH"
exec socat \
	"UNIX-LISTEN:$SOCKET_PATH,fork,unlink-early,mode=0600" \
	"OPENSSL:$HSM_HOST:$HSM_PORT,cert=$CERT_DIR/client.pem,cafile=$CERT_DIR/ca.pem,verify=1,commonname=$HSM_HOST"
