# lib.sh: shared helpers for the ssoosshd-nss integration jobs, sourced by
# direct.sh and systemd.sh inside the container. /it/sh holds these scripts,
# /it/bin the daemon and the fake upstream built on the host, and /it/units
# the shipped units.

SOCK=/run/systemd/userdb/com.github.mnestor.ssoossh
RUN=/run/nss-it
FAILED=0

# check NAME WANT GOT: one assertion, printed either way.
check() {
	if [ "$2" = "$3" ]; then
		echo "PASS $1"
	else
		echo "FAIL $1: want [$2], got [$3]"
		FAILED=1
	fi
}

# check_contains NAME NEEDLE HAYSTACK.
check_contains() {
	case "$3" in
	*"$2"*) echo "PASS $1" ;;
	*)
		echo "FAIL $1: [$2] not in [$3]"
		FAILED=1
		;;
	esac
}

# now_ms prints the time in milliseconds.
now_ms() { date +%s%3N; }

# wait_for PATH: wait up to 10 s for PATH to exist.
wait_for() {
	i=0
	while [ ! -e "$1" ]; do
		i=$((i + 1))
		[ "$i" -gt 100 ] && {
			echo "FAIL timed out waiting for $1"
			exit 1
		}
		sleep 0.1
	done
}

# write_config: the daemon's nss.yaml and fleet key, trusting the fake's
# CA, with the fake's files in $RUN.
write_config() {
	mkdir -p /etc/ssoossh
	install -m 0600 "$RUN/nss.key" /etc/ssoossh/nss.key
	cat >/etc/ssoossh/nss.yaml <<EOC
server: https://127.0.0.1:8443
tls_ca_file: $RUN/ca.pem
api_key_file: /etc/ssoossh/nss.key
EOC
	chmod 0600 /etc/ssoossh/nss.yaml
}

# lookup_asserts: what a host resolves through the daemon, the same in
# both jobs.
lookup_asserts() {
	check "getent passwd alice" "alice:x:60001:60001:Alice Example:/home/alice:/bin/bash" "$(getent passwd alice)"
	check "getent passwd 60001" "alice:x:60001:60001:Alice Example:/home/alice:/bin/bash" "$(getent passwd 60001)"
	check "getent group devs" "devs:x:60100:alice" "$(getent group devs)"
	check "id alice" "uid=60001(alice) gid=60001(alice) groups=60001(alice),60100(devs)" "$(id alice)"
	check_contains "userdbctl user alice names the uid" "UID: 60001" "$(userdbctl user alice 2>&1)"
	check_contains "userdbctl user alice names the service" "Service: com.github.mnestor.ssoossh" "$(userdbctl user alice 2>&1)"
	check "disabled user's shell" "/usr/sbin/nologin" "$(getent passwd bob | cut -d: -f7)"
	check "disabled user's sp_expire" "1" "$(getent shadow bob | cut -d: -f8)"
	getent passwd carol >/dev/null
	check "unknown user is not found" "2" "$?"
}
