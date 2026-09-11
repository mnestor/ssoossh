#!/bin/sh
# Reloads systemd after the units are gone, and removes the service account
# on a real uninstall. See client-postremove.sh for why the account survives
# an upgrade and a configuration-preserving removal.
set -e

account=ssoossh

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi

case "$1" in
0 | purge) ;;
*) exit 0 ;;
esac

if getent passwd "$account" >/dev/null 2>&1; then
	userdel "$account" >/dev/null 2>&1 || true
fi
if getent group "$account" >/dev/null 2>&1; then
	groupdel "$account" >/dev/null 2>&1 || true
fi
