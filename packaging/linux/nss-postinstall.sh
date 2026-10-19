#!/bin/sh
# Makes systemd aware of the ssoossh-nss units this package just installed.
#
# That is all it does. It never enables or starts a unit: the daemon cannot
# run until an administrator has set the server, a TLS trust root and the
# fleet key in /etc/ssoossh/nss.yaml, so a unit started here would only fail
# on every fresh install. And it never edits /etc/nsswitch.conf: whether
# this host consults systemd userdb is the administrator's decision, and the
# daemon reports a missing "systemd" entry in its log and in
# `ssoossh-nss status` rather than changing the file.
set -e

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
