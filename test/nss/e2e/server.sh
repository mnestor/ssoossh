#!/bin/sh
# server.sh: the NSS end-to-end job's server container. /e2e/bin holds
# ssoosshd and e2ehelper built on the host; /e2e/it is test/nss/integration
# for lib.sh's check helpers.
#
#   server.sh start      the container's command: start the provider and
#                        ssoosshd, seed the fleet key, let the scheduled
#                        full sync run once, then restart ssoosshd with the
#                        schedule off so later reads are on demand only.
#                        Touches $E2E/ready when the server is serving.
#   server.sh links      sign the person in through the provider and
#                        assert the login linked the person's posix
#                        account and left the service account unlinked
#   server.sh revoke     revoke the seeded fleet key in the database
#   server.sh logcheck   assert what the server's log shows: the key id
#                        and never its secret, no sync after the restart,
#                        the revoked key's 401 and no lookup after it
#   server.sh dumplog    print the server's log, for a failed run
set -u
. /e2e/it/lib.sh

E2E=/run/e2e
BIN=/e2e/bin
DB=$E2E/ssoossh.db
CONF=$E2E/ssoosshd.yaml
LOG=$E2E/ssoosshd.log

# write_config INTERVAL: ssoosshd's config, with nss.full_sync_interval
# INTERVAL. HTTPS on 8443 with the certificate setup wrote, sqlite, LDAP and
# NSS against the directory container, and OIDC against the loopback
# provider.
write_config() {
	cat >"$CONF" <<EOC
production: false
ssh_key_file: "$E2E/ca-key"
db:
  provider: sqlite
  connection_string: "file:$DB?_busy_timeout=10000"
http:
  address: 0.0.0.0
  port: 8443
  public_url: https://server:8443
  tls:
    certificate_file: "$E2E/tls.crt"
    private_key_file: "$E2E/tls.key"
    # The same pair again, where the server reads it today: TLSConfig
    # embeds CertificateInfo without mapstructure's squash, so the two
    # documented keys above are dropped and ssoosshd serves plain HTTP.
    # Delete this block when server/config/tlsutils is fixed.
    certificateinfo:
      certificate_file: "$E2E/tls.crt"
      private_key_file: "$E2E/tls.key"
authentication:
  client_id: nss-e2e
  client_secret: nss-e2e-secret
  provider_url: http://127.0.0.1:9000
  scopes: "profile email"
  fields:
    username: preferred_username
    email: email
ldap:
  enabled: true
  url: ldap://ldap:389
  bind_dn: cn=admin,dc=example,dc=test
  bind_password: test
  base_dn: dc=example,dc=test
  user_filter: "(uid={{.Username}})"
  id_attribute: entryUUID
nss:
  enabled: true
  disabled_filter: "(employeeType=disabled)"
  sync_interval: 0s
  full_sync_interval: $1
mail:
  enabled: false
logging:
  level: info
  enable_stdout: true
EOC
}

# start_server: ssoosshd in the background, appending to $LOG, until
# /healthz answers (30 s at most).
start_server() {
	"$BIN/ssoosshd" --config "$CONF" >>"$LOG" 2>&1 &
	echo $! >"$E2E/ssoosshd.pid"
	i=0
	until curl -fsS --cacert "$E2E/tls.crt" https://server:8443/healthz >/dev/null 2>&1; do
		i=$((i + 1))
		if [ "$i" -gt 300 ] || ! kill -0 "$(cat "$E2E/ssoosshd.pid")" 2>/dev/null; then
			echo "FAIL ssoosshd did not become healthy"
			tail -40 "$LOG"
			exit 1
		fi
		sleep 0.1
	done
}

# stop_server: SIGTERM, and wait for it to go.
stop_server() {
	pid=$(cat "$E2E/ssoosshd.pid")
	kill -TERM "$pid"
	while kill -0 "$pid" 2>/dev/null; do sleep 0.1; done
}

# wait_log TEXT: wait up to 30 s for TEXT in the server's log.
wait_log() {
	i=0
	until grep -qF "$1" "$LOG"; do
		i=$((i + 1))
		if [ "$i" -gt 300 ]; then
			echo "FAIL timed out waiting for [$1] in the server log"
			tail -40 "$LOG"
			exit 1
		fi
		sleep 0.1
	done
}

case "${1:-}" in
start)
	mkdir -p "$E2E"
	"$BIN/e2ehelper" setup -out "$E2E" -host server >/dev/null || exit 1
	"$BIN/e2ehelper" oidc -listen 127.0.0.1:9000 2>"$E2E/oidc.log" &
	write_config 2s
	start_server
	"$BIN/e2ehelper" seed-key -db "$DB" -key-file "$E2E/nss.key" >"$E2E/key-id" || exit 1
	wait_log "nss directory sync completed"
	stop_server
	write_config 0s
	start_server
	touch "$E2E/ready"
	# Keep the container up for the job; docker stop ends it.
	exec sleep infinity
	;;
links)
	check "ada signs in through the provider" "ada" "$("$BIN/e2ehelper" login -server https://server:8443 -ca "$E2E/tls.crt" -user ada 2>&1)"
	check "the login linked the person's posix account" "linked" "$("$BIN/e2ehelper" linked -db "$DB" -name ada 2>&1)"
	check "the service account stays unlinked" "unlinked" "$("$BIN/e2ehelper" linked -db "$DB" -name svc-backup 2>&1)"
	exit "$FAILED"
	;;
revoke)
	check "the fleet key is revoked" "" "$("$BIN/e2ehelper" revoke-key -db "$DB" -id "$(cat "$E2E/key-id")" 2>&1)"
	exit "$FAILED"
	;;
logcheck)
	id=$(cat "$E2E/key-id")
	secret=$(sed -n 2p "$E2E/nss.key")
	check "the key secret is a fleet secret" "yes" "$(case "$secret" in ssoossh_nss_?*) echo yes ;; *) echo no ;; esac)"
	nss_lines=$(grep -c '/api/nss/' "$LOG")
	check "the access log recorded the NSS lookups" "yes" "$([ "$nss_lines" -gt 0 ] && echo yes || echo no)"
	check "the access log names the key id" "yes" "$(grep '/api/nss/' "$LOG" | grep -qF "$id" && echo yes || echo no)"
	check "no log line holds the key secret" "0" "$(grep -cF "$secret" "$LOG")"
	# late.joiner can only have come from an on-demand read if no sync ran
	# after the restart that turned the schedule off.
	check "no sync ran after the restart" "0" \
		"$(awk '/App server started/ { n = 0; next } /nss directory sync completed/ { n++ } END { print n + 0 }' "$LOG")"
	check "the revoked key's lookup of fresh.name was refused with 401" "yes" \
		"$(grep '/api/nss/user/name/fresh.name' "$LOG" | head -1 | grep -qF 'response.status=401' && echo yes || echo no)"
	# The latch stops the daemon fetching: after the first 401 it sends
	# nothing more, though the latch phase looked up expired entries and the
	# fresh name again.
	check "the daemon sent no lookup after the first 401" "0" \
		"$(grep '/api/nss/' "$LOG" | awk '/response.status=401/ { seen = 1; next } seen { n++ } END { print n + 0 }')"
	exit "$FAILED"
	;;
dumplog)
	grep -v "GIN-debug" "$LOG" | tail -40
	;;
*)
	echo "usage: $0 start | links | revoke | logcheck | dumplog" >&2
	exit 2
	;;
esac
