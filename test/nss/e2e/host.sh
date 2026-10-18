#!/bin/sh
# host.sh: the NSS end-to-end job's host container, a Debian 12 machine
# with nss-systemd on passwd, group and shadow and no systemd as PID 1, so
# ssoosshd-nss binds the userdb socket itself (as test/nss/integration's
# job one). /e2e/bin holds ssoosshd-nss built on the host; /e2e/it is
# test/nss/integration for lib.sh.
#
#   host.sh daemon    the container's command: wait for the fleet key and
#                     the server's SPKI pin to be put in /etc/ssoossh, write
#                     nss.yaml pinned to that SPKI, and run the daemon
#   host.sh synced    what the full sync made resolvable
#   host.sh ondemand  an entry added to the directory after the sync
#   host.sh latch     after the key is revoked: the 401 latch
#   host.sh dumplog   print the daemon's log, for a failed run
set -u
. /e2e/it/lib.sh

E2E=/run/e2e
LOG=$E2E/daemon.log
CACHE=$E2E/cache.db
# entry_cache_timeout: short, so the latch phase can wait out a cached
# entry's lifetime and show the daemon answering it from cache anyway.
TTL=3

# not_found NAME DATABASE KEY: getent's exit status for KEY, 2 is not found.
not_found() {
	getent "$2" "$3" >/dev/null
	check "$1" "2" "$?"
}

case "${1:-}" in
daemon)
	mkdir -p "$E2E"
	wait_for /etc/ssoossh/spki.txt
	wait_for /etc/ssoossh/nss.key
	cat >/etc/ssoossh/nss.yaml <<EOC
server: https://server:8443
tls_spki_pin: ["$(cat /etc/ssoossh/spki.txt)"]
api_key_file: /etc/ssoossh/nss.key
entry_cache_timeout: $TTL
EOC
	chmod 0600 /etc/ssoossh/nss.yaml /etc/ssoossh/nss.key
	SYSTEMD_BYPASS_USERDB=com.github.mnestor.ssoossh exec /e2e/bin/ssoosshd-nss run \
		--listen "$SOCK" --cache-file "$CACHE" 2>>"$LOG"
	;;
synced)
	wait_for "$SOCK"
	check "getent passwd ada" "ada:x:10001:10001:Ada Lovelace:/home/ada:/bin/bash" "$(getent passwd ada)"
	check "getent passwd 10001" "ada:x:10001:10001:Ada Lovelace:/home/ada:/bin/bash" "$(getent passwd 10001)"
	check "id ada" "uid=10001(ada) gid=10001(ada) groups=10001(ada),20001(devs),20002(ops)" "$(id ada 2>&1)"
	check "getent passwd svc-backup" "svc-backup:x:10002:10002:Backup Service:/home/svc-backup:/bin/bash" "$(getent passwd svc-backup)"
	check "id svc-backup" "uid=10002(svc-backup) gid=10002(svc-backup) groups=10002(svc-backup),20001(devs)" "$(id svc-backup 2>&1)"
	check "getent group devs" "devs:x:20001:ada,svc-backup" "$(getent group devs)"
	check "getent group 20001" "devs:x:20001:ada,svc-backup" "$(getent group 20001)"
	check "getent group ops" "ops:x:20002:ada" "$(getent group ops)"
	not_found "uid collision: dup.one is not found" passwd dup.one
	not_found "uid collision: dup.two is not found" passwd dup.two
	not_found "uid collision: uid 10050 is not found" passwd 10050
	check "disabled entry's shell" "/usr/sbin/nologin" "$(getent passwd locked.out | cut -d: -f7)"
	check "disabled entry's sp_expire" "1" "$(getent shadow locked.out | cut -d: -f8)"
	check "active account's sp_expire is unset" "" "$(getent shadow ada | cut -d: -f8)"
	udb=$(userdbctl user svc-backup 2>&1)
	check_contains "userdbctl user svc-backup names the uid" "UID: 10002" "$udb"
	check_contains "userdbctl user svc-backup names the service" "Service: com.github.mnestor.ssoossh" "$udb"
	;;
ondemand)
	wait_for "$SOCK"
	ldapadd -x -H ldap://ldap -D cn=admin,dc=example,dc=test -w test >/dev/null <<EOL
dn: uid=late.joiner,ou=people,dc=example,dc=test
objectClass: inetOrgPerson
objectClass: posixAccount
uid: late.joiner
cn: late.joiner
sn: late.joiner
displayName: Late Joiner
uidNumber: 10003
gidNumber: 10003
homeDirectory: /home/late.joiner
loginShell: /bin/bash
EOL
	check "an entry added after the sync was written to the directory" "0" "$?"
	check "getent passwd late.joiner resolves on demand" "late.joiner:x:10003:10003:Late Joiner:/home/late.joiner:/bin/bash" "$(getent passwd late.joiner)"
	;;
latch)
	wait_for "$SOCK"
	not_found "with the key revoked, a fresh name is not found" passwd fresh.name
	check_contains "the daemon latched on the 401" "the server refused the fleet key" "$(cat "$LOG")"
	# Past the TTL every entry cached above has expired, so only the latch
	# keeps the daemon answering them.
	sleep $((TTL + 1))
	check "with the key revoked, ada answers from the cache" "ada:x:10001:10001:Ada Lovelace:/home/ada:/bin/bash" "$(getent passwd ada)"
	check "with the key revoked, id ada keeps both groups" "uid=10001(ada) gid=10001(ada) groups=10001(ada),20001(devs),20002(ops)" "$(id ada 2>&1)"
	check "with the key revoked, devs answers from the cache" "devs:x:20001:ada,svc-backup" "$(getent group devs)"
	not_found "with the key revoked, the fresh name is still not found" passwd fresh.name
	check "the daemon's log never holds the key secret" "0" "$(grep -cF "$(sed -n 2p /etc/ssoossh/nss.key)" "$LOG")"
	;;
dumplog)
	tail -60 "$LOG"
	exit 0
	;;
*)
	echo "usage: $0 daemon | synced | ondemand | latch | dumplog" >&2
	exit 2
	;;
esac
exit "$FAILED"
