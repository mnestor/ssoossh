#!/bin/sh
# Generate a throwaway CA plus server and client certificates for the mTLS
# tunnel in the network simulation. Idempotent: existing certs are kept.
#
# This models what a real network HSM does for you. CloudHSM, Luna and
# NetHSM authenticate their clients at the appliance; p11-kit's own
# remoting has no transport security whatsoever (its only access control is
# Unix socket permissions), so the simulation has to supply one. That is a
# finding, not a detail: a PKCS#11 proxy is not a substitute for an
# appliance that authenticates you.
set -eu

: "${CERT_DIR:=/certs}"
: "${HSM_CN:=hsm}"
: "${DAYS:=3650}"

mkdir -p "$CERT_DIR"
cd "$CERT_DIR"

if [ -s ca.pem ] && [ -s server.pem ] && [ -s client.pem ]; then
	echo "gen-certs: certificates already present in $CERT_DIR, keeping them."
	exit 0
fi

echo "gen-certs: creating a simulation CA and mTLS keypairs in $CERT_DIR"

openssl req -x509 -newkey rsa:2048 -nodes -days "$DAYS" \
	-keyout ca-key.pem -out ca.pem \
	-subj "/CN=ssoossh-hsm-sim-ca" >/dev/null 2>&1

# socat wants the key and certificate concatenated in one file, so each
# leaf is assembled that way rather than kept as a pair.
gen_leaf() {
	name="$1"; cn="$2"; ext="$3"
	openssl req -newkey rsa:2048 -nodes \
		-keyout "$name-key.pem" -out "$name.csr" \
		-subj "/CN=$cn" >/dev/null 2>&1
	openssl x509 -req -in "$name.csr" -days "$DAYS" \
		-CA ca.pem -CAkey ca-key.pem -CAcreateserial \
		-extfile "$ext" -out "$name-cert.pem" >/dev/null 2>&1
	cat "$name-key.pem" "$name-cert.pem" > "$name.pem"
	rm -f "$name.csr"
}

cat > server.ext <<EOF
subjectAltName = DNS:$HSM_CN, DNS:localhost, IP:127.0.0.1
extendedKeyUsage = serverAuth
EOF
cat > client.ext <<EOF
extendedKeyUsage = clientAuth
EOF

gen_leaf server "$HSM_CN" server.ext
gen_leaf client "ssoosshd" client.ext
rm -f server.ext client.ext

chmod 0644 ca.pem
chmod 0600 ca-key.pem server.pem client.pem server-key.pem client-key.pem
# The bridge sidecar reads client.pem as a non-root user in some setups;
# keep it readable to the group that owns the volume instead of world.
chmod 0640 client.pem

echo "gen-certs: done."
ls -la "$CERT_DIR"
