# Runtime image for ssoosshd, linux-amd64+arm64: ghcr.io/mnestor/ssoossh-server
# (unsuffixed tags). Assembled by goreleaser's dockers_v2 pipe
# (.goreleaser.yml) straight from the server-linux-build binary that step
# already compiled and version-stamped (internal/version) -- there is no
# compile step here, and no ARG to override the stamp with.
#
# base-static, not base-debian12: the default server build is
# CGO_ENABLED=0 and statically linked, so it needs no libc at all. That is
# what removed the glibc/musl image split this project used to carry --
# there is no Dockerfile.musl any more, because a static binary runs on
# Alpine as readily as anywhere else. This base is 2.11MB against
# base-debian12's 20.8MB, and it has no shell, no package manager and no
# libc for anything to link against.
#
# static-debian12 still carries what ssoosshd actually needs:
# /etc/ssl/certs/ca-certificates.crt for HTTPS to an OIDC provider,
# /etc/passwd for the nonroot user, and zoneinfo.
#
# This image cannot dlopen a PKCS#11 module, deliberately -- see
# Dockerfile.pkcs11 for the variant that can, and
# https://mnestor.github.io/ssoossh/operations/ssh-agent/ for why most
# HSM deployments do not need it: `ssh-add -s <module>` puts the token
# behind an ssh-agent, and the key still never leaves the hardware.
#
# Not buildable standalone: `docker build .` has nothing to put at
# linux/$TARGETARCH/ssoosshd unless goreleaser (or something reproducing
# its build context) placed a binary there first. For a local dev build,
# `make server-linux-build-local` (Makefile) does that, then
# `docker compose build` in deploy/ picks it up.
#
# Pinned by digest, not just by tag: `nonroot` is republished whenever
# Debian ships a fix (tzdata, ca-certificates), and a floating tag left each
# image carrying whatever distroless had on its build day, with nothing to
# say when that went stale. Dependabot's docker ecosystem
# (.github/dependabot.yml) opens a PR when the tag moves to a new digest,
# so a base image change is a reviewed commit and the release that follows
# it carries the fix. Keep the tag in the line: Dependabot resolves the new
# digest from it.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETARCH
COPY linux/$TARGETARCH/ssoosshd /usr/local/sbin/ssoosshd
# Reference copies of the mail notification templates the binary embeds, so
# an operator writing a mail.template_dir override can copy one out of the
# running image rather than hunting for the matching source tag. Same path
# and same reasoning as the .deb/.rpm/.apk packages: /usr/share, never an
# active template_dir, since a file in an override directory IS an override
# and would then survive as a stale copy across an upgrade. See
# https://mnestor.github.io/ssoossh/operations/email-notifications/.
COPY server/resources/mail/ /usr/share/ssoossh/mail-templates/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/sbin/ssoosshd"]
CMD ["-c", "/etc/ssoossh/ssoosshd.yaml"]
