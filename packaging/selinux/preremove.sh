#!/bin/sh
# Unloads the ssoosshd-nss policy module on removal, but not on upgrade.
#
# rpm passes the number of instances that will remain to preun: 0 on a real
# removal, 1 or more during an upgrade. Unloading during an upgrade would
# drop the policy the new package has just loaded (the old package's preun
# runs after the new one's postinstall), and confined callers would lose
# the userdb socket until the next install.
set -e

case "${1:-0}" in
0) ;;
*) exit 0 ;;
esac

command -v semodule >/dev/null 2>&1 || exit 0

# Not loaded is not worth failing a removal over: SELinux may be disabled,
# or an operator may have removed it by hand. -X 200 matches postinstall.sh.
semodule -X 200 -r ssoossh_nss >/dev/null 2>&1 || true

# Back to the default labels, so nothing keeps a type the policy no longer
# defines.
for path in /usr/sbin/ssoosshd-nss /etc/ssoossh/nss.yaml /etc/ssoossh/nss.key \
	/etc/ssoossh/nss-overrides.yaml /var/lib/ssoossh-nss /run/ssoossh-nss; do
	if [ -e "$path" ] && command -v restorecon >/dev/null 2>&1; then
		restorecon -R "$path" || true
	fi
done

exit 0
