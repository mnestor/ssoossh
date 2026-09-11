#!/usr/bin/env bash
# Answers one question that nothing in CI can: when sshd runs
# `ssoossh host principals` as AuthorizedPrincipalsCommand on a host with
# SELinux enforcing, does the domain transition differ between /usr/bin and
# the /usr/local/bin compatibility symlink, and does either produce a denial?
#
# WHY THIS IS NOT A CONTAINER TEST. SELinux policy is the host kernel's, and
# a container shares it. The thing under test is the transition sshd_t makes
# when it executes the command, which needs a real systemd-started sshd in
# sshd_t -- an sshd launched from an admin's shell inherits that shell's
# unconfined domain and exercises nothing. So this runs on a host, and it
# should be a throwaway VM.
#
# WHY IT NEEDS NO SSOOSSH SERVER. `ssoossh host principals` is offline: it
# answers from a local file and never opens a socket. So a throwaway CA made
# with ssh-keygen is enough to make sshd actually invoke it, which is the
# only way to observe the transition.
#
# WHAT IT CHANGES, AND PUTS BACK. One sshd_config drop-in, validated with
# `sshd -t` before any reload, removed by an EXIT trap. Reload rather than
# restart, so existing sessions survive. It does not touch the system CA
# trust, any real user's keys, or the installed principals map.
#
#   sudo test/selinux/authorized-principals-label.sh
set -uo pipefail

drop_in=/etc/ssh/sshd_config.d/99-ssoossh-selinux-test.conf
workdir=""
account=ssoossh-principals
fail=0

log()  { printf '\n=== %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }

cleanup() {
	rm -f "$drop_in"
	if [ -n "$workdir" ]; then rm -rf "$workdir"; fi
	# Put sshd back the way it was. If this reload fails the drop-in is
	# already gone, so the running config is the original one either way.
	systemctl reload sshd >/dev/null 2>&1 || systemctl reload ssh >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ------------------------------------------------------------ preflight ----
log "Preflight"

[ "$(id -u)" -eq 0 ] || { note "must run as root"; exit 2; }

command -v getenforce >/dev/null 2>&1 || { note "getenforce not found; this is not an SELinux host"; exit 2; }
mode=$(getenforce)
note "SELinux: $mode"
if [ "$mode" != "Enforcing" ]; then
	note "Refusing to run: a Permissive host logs denials but does not act on"
	note "them, so a clean result here would prove nothing about a real one."
	exit 2
fi

for tool in ssh-keygen sshd ssh semanage matchpathcon ausearch; do
	command -v "$tool" >/dev/null 2>&1 || note "missing (some checks will be skipped): $tool"
done

command -v ssoossh >/dev/null 2>&1 || { note "ssoossh is not installed"; exit 2; }

# ------------------------------------------------------------- labelling ---
log "File context of each path"

for path in /usr/bin/ssoossh /usr/local/bin/ssoossh; do
	if [ -e "$path" ]; then
		note "$(ls -ldZ "$path")"
		if command -v matchpathcon >/dev/null 2>&1; then
			note "  default: $(matchpathcon "$path" 2>/dev/null || echo '(none)')"
		fi
	else
		note "$path: absent"
	fi
done

note ""
note "A difference here is the whole hypothesis: /usr/local/bin and /usr/bin"
note "carry different default file contexts on RHEL, so the domain sshd_t"
note "transitions to when it execs the command can differ between them."

# --------------------------------------------------------- test fixtures ---
log "Building a throwaway CA and certificate"

workdir=$(mktemp -d /tmp/ssoossh-selinux.XXXXXX)
chmod 755 "$workdir"

target_user=${SUDO_USER:-root}
note "certificate will be presented for local account: $target_user"

ssh-keygen -q -t ed25519 -N '' -C ssoossh-selinux-test-ca -f "$workdir/ca" || exit 1
ssh-keygen -q -t ed25519 -N '' -C ssoossh-selinux-test    -f "$workdir/id" || exit 1
# The principal deliberately is NOT the account name, so the only way sshd
# admits the login is by the command returning it. That makes a successful
# login proof the command ran, rather than proof of sshd's own floor.
ssh-keygen -q -s "$workdir/ca" -I ssoossh-selinux-test -n ssoossh-probe \
	-V -5m:+10m "$workdir/id" || exit 1

install -m 0644 "$workdir/ca.pub" "$workdir/trusted-ca.pub"

# A mapping the lookup account can read, mapping the probe principal onto
# the target account.
map="$workdir/principals.yaml"
printf '%s:\n  - ssoossh-probe\n' "$target_user" > "$map"
if getent passwd "$account" >/dev/null 2>&1; then
	chown "root:$account" "$map" && chmod 0640 "$map"
	cmd_user=$account
else
	note "account $account does not exist; falling back to root for the command user"
	chmod 0644 "$map"
	cmd_user=root
fi

# ---------------------------------------------------------------- probe ----
probe_path() {
	local binary=$1
	local label=$2

	log "Probe: $label ($binary)"

	if [ ! -e "$binary" ]; then
		note "absent, skipping"
		return 0
	fi

	cat > "$drop_in" <<-EOF
		TrustedUserCAKeys $workdir/trusted-ca.pub
		AuthorizedPrincipalsCommandUser $cmd_user
		AuthorizedPrincipalsCommand $binary host principals --file $map %u
	EOF

	if ! sshd -t 2>"$workdir/sshd-t.err"; then
		note "sshd rejected the configuration:"
		sed 's/^/      /' "$workdir/sshd-t.err"
		note "This is itself a result: sshd refuses a command it will not run,"
		note "which is what it does for a group- or world-writable path."
		fail=1
		rm -f "$drop_in"
		return 0
	fi

	systemctl reload sshd >/dev/null 2>&1 || systemctl reload ssh >/dev/null 2>&1

	local since
	since=$(date '+%H:%M:%S')
	sleep 1

	if ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
		-o PasswordAuthentication=no -o IdentitiesOnly=yes \
		-o ConnectTimeout=10 \
		-i "$workdir/id" "$target_user@localhost" true 2>"$workdir/ssh.err"; then
		note "RESULT: login succeeded -- sshd ran the command and used its answer"
	else
		note "RESULT: login FAILED"
		sed 's/^/      /' "$workdir/ssh.err" | head -5
		fail=1
	fi

	if command -v ausearch >/dev/null 2>&1; then
		local avc
		avc=$(ausearch -m AVC -ts "$since" 2>/dev/null | grep -i ssoossh)
		if [ -n "$avc" ]; then
			note "AVC denials mentioning ssoossh:"
			printf '%s\n' "$avc" | sed 's/^/      /'
			fail=1
		else
			note "no AVC denials mentioning ssoossh since $since"
		fi
	fi

	rm -f "$drop_in"
	systemctl reload sshd >/dev/null 2>&1 || systemctl reload ssh >/dev/null 2>&1
}

probe_path /usr/bin/ssoossh "packaged path"
probe_path /usr/local/bin/ssoossh "compatibility symlink"

# --------------------------------------------------------------- verdict ---
log "Verdict"
if [ "$fail" -eq 0 ]; then
	note "Both paths ran clean. The move to /usr/bin introduces no SELinux"
	note "regression, and no file-context rules are needed in the package."
else
	note "Something denied, failed or was refused above. Capture the whole"
	note "output: the AVC's scontext and tcontext say which domain and which"
	note "file label were involved, which is what decides whether the fix is"
	note "a file-context rule (semanage fcontext + restorecon) or something"
	note "larger."
fi
exit "$fail"
