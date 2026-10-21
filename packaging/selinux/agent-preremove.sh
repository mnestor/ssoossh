#!/bin/sh
# Unloads the ssoossh_agent policy module on removal, but not on upgrade.
#
# rpm passes the number of instances that will remain to preun: 0 on a real
# removal, 1 or more during an upgrade. Unloading during an upgrade would
# drop the policy the new package has just loaded (the old package's preun
# runs after the new one's postinstall).
set -e

case "${1:-0}" in
0) ;;
*) exit 0 ;;
esac

command -v semodule >/dev/null 2>&1 || exit 0

# Only this package's copy, at the priority agent-postinstall.sh loaded it
# at: a copy an operator installed by hand at another priority is theirs
# and stays. Not loaded is not worth failing a removal over: SELinux may be
# disabled, or an operator may have removed it by hand.
#
# A running agent keeps running: SELinux checks the exec, not the process
# afterwards. Its next start fails until the module is back.
semodule -X 200 -r ssoossh_agent >/dev/null 2>&1 || true

exit 0
