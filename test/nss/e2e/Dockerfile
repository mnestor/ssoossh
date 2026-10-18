# The NSS end-to-end image (test/nss/e2e, scripts/nss-e2e.sh): Debian 12
# with what all three of the job's containers need, so one image serves
# them all. The directory container runs slapd from it, the server
# container runs ssoosshd and the helper, and the host container runs
# ssoosshd-nss behind nss-systemd. The binaries and the scripts are mounted
# in at run time, not built in.
FROM debian:12

# slapd and ldap-utils the throwaway OpenLDAP and the ldapadd the host
# script runs; libnss-systemd and systemd-userdbd nss-systemd and
# userdbctl, as in test/nss/integration; procps for pgrep; curl for the
# server's health check.
RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
		slapd ldap-utils libnss-systemd systemd systemd-userdbd procps curl \
	&& rm -rf /var/lib/apt/lists/*

# The platform floor's nsswitch.conf: systemd after files on passwd, group
# and shadow.
RUN sed -i -E 's/^(passwd|group|shadow):.*/\1: files systemd/' /etc/nsswitch.conf \
	&& grep -E '^(passwd|group|shadow): files systemd$' /etc/nsswitch.conf | wc -l | grep -qx 3

# Mount points for the scripts (and test/nss/integration's lib.sh) and the
# binaries.
RUN mkdir -p /e2e/sh /e2e/it /e2e/bin
