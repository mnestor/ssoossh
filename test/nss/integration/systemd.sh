#!/bin/sh
# systemd.sh: integration job two, run inside a container booted with
# systemd as PID 1. Installs the daemon and the shipped units the way the
# package does, and proves that socket activation starts the daemon on the
# first lookup and queues lookups across `systemctl restart`.
set -u
. /it/sh/lib.sh

install -m 0755 /it/bin/ssoossh-nss /usr/sbin/ssoossh-nss
install -m 0644 /it/units/ssoossh-nss.socket /it/units/ssoossh-nss.service /usr/lib/systemd/system/
mkdir -p "$RUN"
systemd-run --quiet --unit=nss-fakeupstream /it/bin/nss-fakeupstream -out "$RUN"
wait_for "$RUN/nss.key"
write_config
systemctl daemon-reload
systemctl start ssoossh-nss.socket

check "the service waits for its first lookup" "inactive" "$(systemctl is-active ssoossh-nss.service)"
lookup_asserts
check "the first lookup started the service" "active" "$(systemctl is-active ssoossh-nss.service)"
# The unit's resource limits are in force on the running daemon, and the
# lookups above passed under them.
pid=$(systemctl show -p MainPID --value ssoossh-nss.service)
check "the daemon runs with LimitNOFILE" "8192" "$(awk '/^Max open files/ {print $5}' "/proc/$pid/limits")"
check "the service has MemoryMax" "536870912" "$(systemctl show -p MemoryMax --value ssoossh-nss.service)"
check "the service has TasksMax" "4096" "$(systemctl show -p TasksMax --value ssoossh-nss.service)"
check "the daemon has GOMEMLIMIT" "GOMEMLIMIT=400MiB" "$(tr '\0' '\n' <"/proc/$pid/environ" | grep '^GOMEMLIMIT=')"
collision_asserts "$(ssoossh-nss status 2>&1)" "$(journalctl -u ssoossh-nss --no-pager -o cat)"

# Lookups every 20 ms while the service restarts three times: none may
# fail, because systemd keeps the socket and queues them.
before=$(systemctl show -p InvocationID --value ssoossh-nss.service)
(
	i=0
	misses=0
	while [ "$i" -lt 150 ]; do
		getent passwd alice >/dev/null || misses=$((misses + 1))
		i=$((i + 1))
		sleep 0.02
	done
	echo "$misses" >"$RUN/misses"
) &
LOOP=$!
for n in 1 2 3; do
	sleep 0.5
	systemctl restart ssoossh-nss.service
done
wait "$LOOP"
after=$(systemctl show -p InvocationID --value ssoossh-nss.service)
check "the service was restarted" "yes" "$([ "$before" != "$after" ] && echo yes || echo no)"
check "no lookup failed across three restarts" "0" "$(cat "$RUN/misses")"

# A lookup made while the service is stopped starts it again.
systemctl stop ssoossh-nss.service
check "the service is stopped" "inactive" "$(systemctl is-active ssoossh-nss.service)"
check "a lookup to the stopped service still resolves" "60001" "$(getent passwd alice | cut -d: -f3)"
check "that lookup started the service" "active" "$(systemctl is-active ssoossh-nss.service)"

if [ "$FAILED" != 0 ]; then
	echo "--- daemon journal"
	journalctl -u ssoossh-nss --no-pager -o cat | tail -40
	echo "--- upstream journal"
	journalctl -u nss-fakeupstream --no-pager -o cat | tail -20
fi
exit "$FAILED"
