# Runtime image for ssoosshd, glibc/linux-amd64+arm64: ghcr.io/mnestor/ssoosshd
# (unsuffixed tags). Assembled by goreleaser's dockers_v2 pipe
# (.goreleaser.yml) straight from the server-linux-build binary that step
# already compiled and version-stamped (internal/version) -- there is no
# compile step here, and no ARG to override the stamp with. See
# Dockerfile.musl for the Alpine/musl counterpart image, and
# docs/operations/hsm.md for why the two exist (musl for a bind-mounted
# PKCS#11 module built against musl, not host-OS compatibility -- Docker
# already abstracts that away).
#
# Not buildable standalone: `docker build .` has nothing to put at
# linux/$TARGETARCH/ssoosshd unless goreleaser (or something reproducing
# its build context) placed a binary there first. For a local dev build,
# `make server-linux-build-local` (Makefile) does that, then
# `docker compose build` in deploy/ picks it up.
#
# base-debian12 (not static-) because ssoosshd is dynamically linked (cgo,
# dlopen for PKCS#11 modules). To use an HSM in-container, mount the
# PKCS#11 module and its deps into the image (see docs/operations/hsm.md).
FROM gcr.io/distroless/base-debian12:nonroot
ARG TARGETARCH
COPY linux/$TARGETARCH/ssoosshd /usr/local/sbin/ssoosshd
# Reference copies of the mail notification templates the binary embeds, so
# an operator writing a mail.template_dir override can copy one out of the
# running image rather than hunting for the matching source tag. Same path
# and same reasoning as the .deb/.rpm/.apk packages: /usr/share, never an
# active template_dir, since a file in an override directory IS an override
# and would then survive as a stale copy across an upgrade. See
# docs/operations/email-notifications.md.
COPY server/resources/mail/ /usr/share/ssoossh/mail-templates/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/sbin/ssoosshd"]
CMD ["-c", "/etc/ssoossh/ssoosshd.yaml"]
