#!/bin/sh
# Stops the service before its binary and unit go away, on uninstall only.
#
# Not on upgrade: systemd keeps a running service across a package upgrade,
# and stopping it here would turn every upgrade into an outage that nothing
# restarts. rpm passes 0 to preun on uninstall; dpkg passes "remove" or
# "purge" to prerm, and "upgrade" when it is one.
set -e

case "$1" in
0 | remove | purge) ;;
*) exit 0 ;;
esac

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl stop ssoosshd.service >/dev/null 2>&1 || true
	systemctl disable ssoosshd.service >/dev/null 2>&1 || true
fi
