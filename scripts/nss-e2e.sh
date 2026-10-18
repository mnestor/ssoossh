#!/bin/sh
# Runs the NSS end-to-end job (test/nss/e2e) in docker, the same way CI's
# nss-integration workflow does: a throwaway OpenLDAP, the real ssoosshd
# over HTTPS with sqlite, NSS and LDAP on, and the real ssoosshd-nss behind
# nss-systemd on a Debian 12 host, each in its own container on a private
# network.
#
#   scripts/nss-e2e.sh
#
# Builds ssoosshd (without the web UI), ssoosshd-nss and the test helper
# for linux/amd64 into build/nss-e2e/, builds the image
# ssoossh-nss-e2e:debian12 from test/nss/e2e/Dockerfile, and runs three
# --rm containers on the network ssoossh-nss-e2e-<pid>. The containers and
# the network are removed when the job ends, pass or fail. The image is
# left for the next run; `docker rmi ssoossh-nss-e2e:debian12` removes it.
set -eu

image=ssoossh-nss-e2e:debian12
bin=build/nss-e2e
tag=ssoossh-nss-e2e-$$
net=$tag
ldap=$tag-ldap
server=$tag-server
host=$tag-host

if [ ! -f test/nss/e2e/Dockerfile ]; then
	echo "nss-e2e: run from the repository root" >&2
	exit 1
fi

# The release build's settings for the two shipped binaries (cgo off, the
# pinned FIPS module); ssoosshd without the embedded web UI, which the job
# never loads. NSS_IT_PREBUILT=1 uses binaries already in build/nss-e2e/,
# which is how CI runs it.
if [ "${NSS_IT_PREBUILT:-}" = 1 ]; then
	for f in ssoosshd ssoosshd-nss e2ehelper; do
		[ -f "$bin/$f" ] || {
			echo "nss-e2e: NSS_IT_PREBUILT=1 but $bin/$f is missing" >&2
			exit 1
		}
		chmod 0755 "$bin/$f"
	done
else
	mkdir -p "$bin"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFIPS140=v1.0.0 go build -trimpath -tags exclude_frontend -o "$bin/ssoosshd" ./cmd/ssoosshd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFIPS140=v1.0.0 go build -trimpath -o "$bin/ssoosshd-nss" ./cmd/ssoosshd-nss
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$bin/e2ehelper" ./test/nss/e2ehelper
fi
docker build -q -t "$image" test/nss/e2e >/dev/null

# cleanup stops the three containers (--rm removes them) and the network.
cleanup() {
	for c in "$host" "$server" "$ldap"; do
		docker stop -t 5 "$c" >/dev/null 2>&1 || true
	done
	docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create "$net" >/dev/null

mounts="-v $PWD/test/nss/e2e:/e2e/sh:ro -v $PWD/test/nss/integration:/e2e/it:ro -v $PWD/$bin:/e2e/bin:ro"
# shellcheck disable=SC2086 # $mounts is several words on purpose.
docker run -d --rm --name "$ldap" --network "$net" --network-alias ldap $mounts "$image" /e2e/sh/ldap.sh >/dev/null
# shellcheck disable=SC2086
docker run -d --rm --name "$server" --hostname server --network "$net" --network-alias server $mounts "$image" /e2e/sh/server.sh start >/dev/null
# shellcheck disable=SC2086
docker run -d --rm --name "$host" --hostname host --network "$net" $mounts "$image" /e2e/sh/host.sh daemon >/dev/null

# The server comes up, seeds the key, syncs once and restarts: 90 s at most.
i=0
until docker exec "$server" test -f /run/e2e/ready 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 900 ] || ! docker inspect "$server" >/dev/null 2>&1; then
		echo "FAIL the server did not get ready"
		docker logs "$server" 2>&1 | tail -40
		docker exec "$server" /e2e/sh/server.sh dumplog 2>/dev/null || true
		exit 1
	fi
	sleep 0.1
done

# The host trusts the server by its SPKI and holds the fleet key, both
# handed over container to container; the secret never reaches this
# shell's output.
docker exec "$server" cat /run/e2e/spki.txt | docker exec -i "$host" sh -c 'mkdir -p /etc/ssoossh && cat >/etc/ssoossh/spki.txt.tmp && mv /etc/ssoossh/spki.txt.tmp /etc/ssoossh/spki.txt'
docker exec "$server" cat /run/e2e/nss.key | docker exec -i "$host" sh -c 'umask 077 && cat >/etc/ssoossh/nss.key.tmp && mv /etc/ssoossh/nss.key.tmp /etc/ssoossh/nss.key'

out=$(mktemp)
status=0
# phase NAME CONTAINER SCRIPT ARG: one phase, its lines kept for the count.
phase() {
	echo "=== $1"
	docker exec "$2" "/e2e/sh/$3" "$4" >"$out.phase" 2>&1 || status=1
	cat "$out.phase"
	cat "$out.phase" >>"$out"
}
phase "a person signs in; the login links the person's account" "$server" server.sh links
phase "what the scheduled full sync made resolvable" "$host" host.sh synced
phase "an entry added after the sync resolves on demand" "$host" host.sh ondemand
phase "the fleet key is revoked in the database" "$server" server.sh revoke
phase "the 401 latch: the cache keeps answering, a fresh name is not found" "$host" host.sh latch
phase "the server's access log" "$server" server.sh logcheck

passed=$(grep -c '^PASS ' "$out" || true)
failed=$(grep -c '^FAIL ' "$out" || true)
rm -f "$out" "$out.phase"
if [ "$status" != 0 ] || [ "$failed" != 0 ]; then
	echo "--- server log"
	docker exec "$server" /e2e/sh/server.sh dumplog || true
	echo "--- daemon log"
	docker exec "$host" /e2e/sh/host.sh dumplog || true
	echo "nss-e2e: FAIL ($passed passed, $failed failed)"
	exit 1
fi
echo "nss-e2e: PASS ($passed checks)"
