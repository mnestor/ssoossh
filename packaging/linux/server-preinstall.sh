#!/bin/sh
# Creates the system account ssoosshd.service runs as.
#
# The unit this package ships names User=ssoossh and Group=ssoossh, so
# without this the unit is installed and cannot start. Same preinstall
# reasoning as the client hook: /etc/ssoossh and the state directory are
# chowned to this account by the operator, and the config directory holds
# the CA private key.
set -e

account=ssoossh

nologin=/usr/sbin/nologin
[ -x "$nologin" ] || nologin=/sbin/nologin
[ -x "$nologin" ] || nologin=/bin/false

if ! getent group "$account" >/dev/null 2>&1; then
	groupadd --system "$account"
fi

if ! getent passwd "$account" >/dev/null 2>&1; then
	useradd --system --gid "$account" \
		--no-create-home --home-dir /nonexistent \
		--shell "$nologin" --comment "ssoossh certificate authority" "$account"
fi
