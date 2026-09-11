#!/bin/sh
# Removes the principals lookup account, but only on a real uninstall.
#
# The removal half of an upgrade must not take the account away: the new
# package's files are owned by that group, and rpm runs the old package's
# postun after the new package has already been unpacked.
#
# The two package managers signal "this is the last one" differently. rpm
# passes 0 to postun on uninstall and 1 on upgrade; dpkg passes "purge" to
# postrm when configuration goes too, and "remove" when it stays -- and if
# /etc/ssoossh/principals.yaml is staying, so must the group that owns it.
set -e

account=ssoossh-principals

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
