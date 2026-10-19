#!/bin/sh
# Reloads systemd after the ssoosshd-nss units are gone.
#
# The cache in /var/lib/ssoossh-nss is left for the administrator: systemd
# owns that directory through StateDirectory=, and a reinstall resolves the
# users it knew while the server is unreachable.
set -e

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
