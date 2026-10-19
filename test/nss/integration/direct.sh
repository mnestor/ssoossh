#!/bin/sh
# direct.sh: integration job one. No systemd as PID 1: the daemon binds the
# userdb socket itself (--listen) and nss-systemd, finding no
# systemd-userdbd, connects to it directly.
set -u
. /it/sh/lib.sh

mkdir -p "$RUN"
/it/bin/nss-fakeupstream -out "$RUN" 2>"$RUN/upstream.log" &
wait_for "$RUN/nss.key"
write_config

SYSTEMD_BYPASS_USERDB=com.github.mnestor.ssoossh /it/bin/ssoossh-nss run --listen "$SOCK" --cache-file "$RUN/cache.db" 2>"$RUN/daemon.log" &
DAEMON=$!
wait_for "$SOCK"

lookup_asserts
collision_asserts "$(/it/bin/ssoossh-nss status 2>&1)" "$(cat "$RUN/daemon.log")"

# With the daemon gone, killed so its socket file is left behind, a lookup
# is a quick not-found rather than a wait.
kill -KILL "$DAEMON"
wait "$DAEMON" 2>/dev/null
check "stale socket left by the killed daemon" "yes" "$([ -S "$SOCK" ] && echo yes || echo no)"
start=$(now_ms)
getent passwd alice >/dev/null
rc=$?
took=$(($(now_ms) - start))
check "lookup with the daemon stopped is not found" "2" "$rc"
check "lookup with the daemon stopped is quick (${took} ms)" "yes" "$([ "$took" -lt 1000 ] && echo yes || echo no)"

if [ "$FAILED" != 0 ]; then
	echo "--- daemon log"
	cat "$RUN/daemon.log"
	echo "--- upstream log"
	cat "$RUN/upstream.log"
fi
exit "$FAILED"
