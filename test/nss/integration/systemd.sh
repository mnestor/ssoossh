#!/bin/sh
# systemd.sh: integration job two, run inside a container booted with
# systemd as PID 1. Installs the daemon and the shipped units the way the
# package does, and proves that socket activation starts the daemon on the
# first lookup and queues lookups across `systemctl restart`.
set -u
. /it/sh/lib.sh

install -m 0755 /it/bin/ssoosshd-nss /usr/sbin/ssoosshd-nss
install -m 0644 /it/units/ssoosshd-nss.socket /it/units/ssoosshd-nss.service /usr/lib/systemd/system/
mkdir -p "$RUN"
systemd-run --quiet --unit=nss-fakeupstream /it/bin/nss-fakeupstream -out "$RUN"
wait_for "$RUN/nss.key"
write_config
systemctl daemon-reload
systemctl start ssoosshd-nss.socket

check "the service waits for its first lookup" "inactive" "$(systemctl is-active ssoosshd-nss.service)"
lookup_asserts
check "the first lookup started the service" "active" "$(systemctl is-active ssoosshd-nss.service)"

# Lookups every 20 ms while the service restarts three times: none may
# fail, because systemd keeps the socket and queues them.
before=$(systemctl show -p InvocationID --value ssoosshd-nss.service)
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
	systemctl restart ssoosshd-nss.service
done
wait "$LOOP"
after=$(systemctl show -p InvocationID --value ssoosshd-nss.service)
check "the service was restarted" "yes" "$([ "$before" != "$after" ] && echo yes || echo no)"
check "no lookup failed across three restarts" "0" "$(cat "$RUN/misses")"

# A lookup made while the service is stopped starts it again.
systemctl stop ssoosshd-nss.service
check "the service is stopped" "inactive" "$(systemctl is-active ssoosshd-nss.service)"
check "a lookup to the stopped service still resolves" "60001" "$(getent passwd alice | cut -d: -f3)"
check "that lookup started the service" "active" "$(systemctl is-active ssoosshd-nss.service)"

if [ "$FAILED" != 0 ]; then
	echo "--- daemon journal"
	journalctl -u ssoosshd-nss --no-pager -o cat | tail -40
	echo "--- upstream journal"
	journalctl -u nss-fakeupstream --no-pager -o cat | tail -20
fi
exit "$FAILED"
