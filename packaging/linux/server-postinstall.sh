#!/bin/sh
# Makes systemd aware of the units this package just installed.
#
# Deliberately does not enable or start anything. ssoosshd will not run
# without a configured CA key and identity provider, so a unit started at
# install time would only fail loudly on every fresh install. Installing
# the unit file is the package's job; deciding to run it is the operator's.
set -e

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
