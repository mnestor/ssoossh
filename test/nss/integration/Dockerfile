# The ssoossh-nss integration image (test/nss, scripts/nss-integration.sh):
# Debian 12 with nss-systemd and userdbctl, and systemd as an init that the
# socket-activation job boots. The daemon, the fake upstream and the
# scripts are mounted in at run time, not built in, so one image serves
# every run.
FROM debian:12

# libnss-systemd is nss-systemd; systemd-userdbd brings userdbctl (and, in
# the systemd job, systemd-userdbd, as EL10 runs it); systemd the init;
# procps for pgrep. Nothing else: the image proves the daemon works with
# what a minimal host has.
RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
		libnss-systemd systemd systemd-sysv systemd-userdbd procps \
	&& rm -rf /var/lib/apt/lists/*

# What the platform floor asks of nsswitch.conf: systemd after files on
# passwd, group and shadow. libnss-systemd's postinst may already have
# added some of it; this sets all three lines outright.
RUN sed -i -E 's/^(passwd|group|shadow):.*/\1: files systemd/' /etc/nsswitch.conf \
	&& grep -E '^(passwd|group|shadow): files systemd$' /etc/nsswitch.conf | wc -l | grep -qx 3

# Mount points for the scripts, the binaries and the units.
RUN mkdir -p /it/sh /it/bin /it/units
