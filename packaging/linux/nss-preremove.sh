#!/bin/sh
# Stops and disables the daemon and its socket before they go away, on
# uninstall only.
#
# The socket matters more than the service. A socket left listening with no
# binary behind it accepts every lookup and starts a service that cannot
# run, so each caller waits out systemd's 45 s call timeout instead of
# getting a quick "not found".
#
# Not on upgrade: systemd keeps both running across a package upgrade.
# rpm passes 0 to preun on uninstall; dpkg passes "remove" or "purge" to
# prerm, and "upgrade" when it is one.
set -e

case "$1" in
0 | remove | purge) ;;
*) exit 0 ;;
esac

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl stop ssoosshd-nss.socket ssoosshd-nss.service >/dev/null 2>&1 || true
	systemctl disable ssoosshd-nss.socket ssoosshd-nss.service >/dev/null 2>&1 || true
fi
