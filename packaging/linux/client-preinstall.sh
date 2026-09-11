#!/bin/sh
# Creates the unprivileged account sshd runs the principals lookup as.
#
# preinstall rather than postinstall because both rpm and dpkg apply file
# ownership as they unpack. A group created afterwards is too late: the
# mapping file this package ships names that group, and would land owned by
# root instead -- leaving the lookup account unable to read the one file it
# exists to read, which fails silently (the lookup answers with the account
# name alone and exits 0).
set -e

account=ssoossh-principals

nologin=/usr/sbin/nologin
[ -x "$nologin" ] || nologin=/sbin/nologin
[ -x "$nologin" ] || nologin=/bin/false

# Idempotent: an upgrade re-runs this hook, and an operator may already have
# created the account by hand from the instructions that predate the package
# doing it.
if ! getent group "$account" >/dev/null 2>&1; then
	groupadd --system "$account"
fi

if ! getent passwd "$account" >/dev/null 2>&1; then
	useradd --system --gid "$account" \
		--no-create-home --home-dir /nonexistent \
		--shell "$nologin" --comment "ssoossh principals lookup" "$account"
fi
