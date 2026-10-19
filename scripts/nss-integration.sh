#!/bin/sh
# Runs the ssoossh-nss integration jobs (test/nss/integration) in docker,
# the same way CI's nss-integration workflow does.
#
#   scripts/nss-integration.sh            # both jobs
#   scripts/nss-integration.sh direct     # job one: the daemon self-binds
#   scripts/nss-integration.sh systemd    # job two: systemd as PID 1
#
# Builds the daemon and the fake upstream for linux/amd64 into
# build/nss-it/, builds the image ssoossh-nss-it:debian12 from
# test/nss/integration/Dockerfile, and runs each job in a --rm container.
# Job two boots systemd in a privileged container; the container is
# removed when the job ends, pass or fail. The image is left for the next
# run; `docker rmi ssoossh-nss-it:debian12` removes it.
set -eu

image=ssoossh-nss-it:debian12
bin=build/nss-it
jobs=${1:-all}
case "$jobs" in
all | direct | systemd) ;;
*)
	echo "usage: $0 [direct | systemd]" >&2
	exit 2
	;;
esac

if [ ! -f test/nss/integration/Dockerfile ]; then
	echo "nss-integration: run from the repository root" >&2
	exit 1
fi

# The release build's settings (.goreleaser.yml): cgo off, the pinned
# FIPS module. NSS_IT_PREBUILT=1 uses binaries already in build/nss-it/,
# which is how CI runs it: the Go toolchain lives in the build image, and
# the jobs need a bare runner's docker.
if [ "${NSS_IT_PREBUILT:-}" = 1 ]; then
	for f in ssoossh-nss nss-fakeupstream; do
		[ -f "$bin/$f" ] || {
			echo "nss-integration: NSS_IT_PREBUILT=1 but $bin/$f is missing" >&2
			exit 1
		}
		chmod 0755 "$bin/$f"
	done
else
	mkdir -p "$bin"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFIPS140=v1.0.0 go build -trimpath -o "$bin/ssoossh-nss" ./cmd/ssoossh-nss
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$bin/nss-fakeupstream" ./test/nss/fakeupstream
fi
docker build -q -t "$image" test/nss/integration >/dev/null

mounts="-v $PWD/test/nss/integration:/it/sh:ro -v $PWD/$bin:/it/bin:ro -v $PWD/deploy:/it/units:ro"
status=0

if [ "$jobs" != systemd ]; then
	echo "=== job one: daemon self-binds the userdb socket (no systemd)"
	# shellcheck disable=SC2086 # $mounts is several words on purpose.
	docker run --rm $mounts "$image" /it/sh/direct.sh || status=1
fi

if [ "$jobs" != direct ]; then
	echo "=== job two: systemd as PID 1, socket activation"
	name=ssoossh-nss-it-systemd-$$
	# Privileged with tmpfs /run and /run/lock is what systemd needs to
	# boot as PID 1 in a container on a cgroup v2 host.
	# shellcheck disable=SC2086
	docker run -d --rm --name "$name" --privileged --tmpfs /run --tmpfs /run/lock \
		$mounts "$image" /sbin/init >/dev/null
	trap 'docker stop -t 5 "$name" >/dev/null 2>&1 || true' EXIT
	i=0
	until state=$(docker exec "$name" systemctl is-system-running 2>/dev/null) || [ "$state" = degraded ]; do
		i=$((i + 1))
		if [ "$i" -gt 60 ]; then
			echo "FAIL systemd did not finish booting (state: ${state:-none})"
			status=1
			break
		fi
		sleep 1
	done
	if [ "$i" -le 60 ]; then
		docker exec "$name" /it/sh/systemd.sh || status=1
	fi
fi

if [ "$status" = 0 ]; then echo "nss-integration: PASS"; else echo "nss-integration: FAIL"; fi
exit "$status"
