#!/bin/sh
# Loads the ssoossh-nss policy module and relabels what it labels.
#
# Runs on install and on upgrade: semodule -i replaces a module of the same
# name, so loading needs no separate upgrade path.
set -e

PP=/usr/share/selinux/packages/ssoossh-nss/ssoossh_nss.pp

# A host with SELinux disabled still loads modules (the policy takes effect
# once SELinux is enabled), so this does not check getenforce. Refusing here
# would leave a host that enables SELinux later with the package installed
# and no module loaded.
if ! command -v semodule >/dev/null 2>&1; then
	echo "ssoossh-nss-selinux: semodule not found; policy not loaded" >&2
	echo "  install policycoreutils, then: semodule -X 200 -i $PP" >&2
	exit 0
fi

# Priority 200 is the vendor slot the EL %selinux_modules_install macro
# uses, so a site's own module of the same name at the default 400 still
# wins. preremove.sh must name the same priority to find it.
if ! semodule -X 200 -i "$PP"; then
	echo "ssoossh-nss-selinux: failed to load $PP" >&2
	echo "  confined callers such as sshd will still be denied the userdb socket" >&2
	exit 1
fi

# Files installed before the module existed carry the default labels (the
# binary bin_t, which runs unconfined). Each path may not exist yet; the
# daemon's package may come later, and systemd makes the two directories
# at the service's first start, labelled from the policy by then.
for path in /usr/sbin/ssoossh-nss /etc/ssoossh/nss.yaml /etc/ssoossh/nss.key \
	/etc/ssoossh/nss-overrides.yaml /var/lib/ssoossh-nss /run/ssoossh-nss; do
	if [ -e "$path" ] && command -v restorecon >/dev/null 2>&1; then
		restorecon -R "$path" || true
	fi
done

# A socket systemd is already holding keeps the label it was created with,
# the unconfined one, until the socket unit restarts, and a running daemon
# keeps its domain until it restarts; restarting the socket stops the
# service too, and the next lookup starts it confined. First install only
# (rpm passes 1; 2 or more on upgrade): after an upgrade the labels are
# already right, and a restart would turn lookups made in that moment into
# "not found". try-restart touches only a running unit, so a host that has
# not enabled the daemon is left alone.
if [ "${1:-1}" = 1 ] && [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl try-restart ssoossh-nss.socket >/dev/null 2>&1 || true
fi

exit 0
