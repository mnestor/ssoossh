#!/bin/sh
# Loads the ssoossh_agent policy module, which lets ssoossh-agent.service
# run ssh-agent under enforcing. See ssoossh_agent.te.
#
# Runs on install and on upgrade: semodule -i replaces a module of the same
# name at the same priority, so loading needs no separate upgrade path.
set -e

PP=/usr/share/selinux/packages/ssoosshd/ssoossh_agent.pp
NAME=ssoossh_agent

# A host with SELinux disabled still loads modules (the policy takes effect
# once SELinux is enabled), so this does not check getenforce.
if ! command -v semodule >/dev/null 2>&1; then
	echo "ssoosshd-selinux: semodule not found; policy not loaded" >&2
	echo "  install policycoreutils, then: semodule -X 200 -i $PP" >&2
	exit 0
fi

# Priority 200 is the vendor slot the EL %selinux_modules_install macro
# uses. agent-preremove.sh must name the same priority to find it.
if ! semodule -X 200 -i "$PP"; then
	echo "ssoosshd-selinux: failed to load $PP" >&2
	echo "  ssoossh-agent.service will fail to start under enforcing (status=203/EXEC)" >&2
	exit 1
fi

# Modules of one name at different priorities coexist, and only the highest
# is active. The likeliest other copy is one an operator built by hand from
# the ssh-agent operations page, which `semodule -i` puts at 400, above this
# one. It grants the same rule, so nothing breaks; it is the operator's, so
# it is left alone and named instead.
semodule --list-modules=full 2>/dev/null |
	awk -v name="$NAME" '$2 == name && $1 + 0 > 200 { print $1 }' |
	while read -r other; do
		echo "ssoosshd-selinux: $NAME is also installed at priority $other, which overrides this package's copy" >&2
		echo "  if it was built by hand, remove it with: semodule -X $other -r $NAME" >&2
	done

# Deliberately no restart of ssoossh-agent: a restarted agent comes back
# without its key, and ssoosshd then fails every signature. A unit that
# failed to start is not running and picks the policy up at its next start.
exit 0
