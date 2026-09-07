#!/bin/sh
# Stand up the simulated network HSM: a p11-kit PKCS#11 server in front of
# a SoftHSM2 token, published over an mTLS TCP socket.
#
# Two hops, and the split matters:
#
#   p11-kit server  ->  a Unix socket. This is the ONLY transport p11-kit
#                       offers. `p11-kit server --help` has no host, port or
#                       TLS option; its entire access control model is the
#                       socket's file permissions (-u/--user, -g/--group).
#   socat OPENSSL   ->  the network hop, with mutual TLS bolted on here
#                       because p11-kit does not provide one.
#
# That second line is the point of the whole simulation. A real network HSM
# authenticates its clients itself; a PKCS#11 proxy makes you build that,
# and if you skip it the token is reachable by anyone who can open the port.
set -eu

: "${TOKEN_DIR:=/hsm/tokens}"
: "${LIB_DIR:=/hsm/lib}"
: "${TOKEN_LABEL:=ssoossh-ca}"
: "${CERT_DIR:=/certs}"
: "${LISTEN_PORT:=12345}"
: "${SOCKET_PATH:=/run/p11/server.sock}"

MODULE=/usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so
SOFTHSM2_CONF="$LIB_DIR/softhsm2.conf"
export SOFTHSM2_CONF

mkdir -p "$(dirname "$SOCKET_PATH")"
rm -f "$SOCKET_PATH"

echo "hsm-serve: starting p11-kit server for token '$TOKEN_LABEL'"
# p11-kit server daemonizes and prints shell assignments; the socket path is
# fixed with -n so nothing has to parse that output.
p11-kit server \
	--provider "$MODULE" \
	-n "$SOCKET_PATH" \
	"pkcs11:token=$TOKEN_LABEL" >/dev/null

# Wait for the socket rather than racing it.
i=0
while [ ! -S "$SOCKET_PATH" ]; do
	i=$((i + 1))
	[ "$i" -gt 50 ] && { echo "hsm-serve: p11-kit server never created $SOCKET_PATH" >&2; exit 1; }
	sleep 0.1
done
echo "hsm-serve: p11-kit listening on $SOCKET_PATH"

# verify=1 makes client certificates mandatory, so this is mutual TLS and
# not just an encrypted pipe. socat is the container's main process: killing
# this container is how the simulation injects an HSM outage.
echo "hsm-serve: publishing over mTLS on :$LISTEN_PORT"
exec socat \
	"OPENSSL-LISTEN:$LISTEN_PORT,reuseaddr,fork,cert=$CERT_DIR/server.pem,cafile=$CERT_DIR/ca.pem,verify=1" \
	"UNIX-CONNECT:$SOCKET_PATH"
