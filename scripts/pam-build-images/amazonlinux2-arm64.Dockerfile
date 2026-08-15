# Build environment for linking pam_ssoossh.so against the arm64 glibc
# floor (glibc 2.26). Used only by scripts/build-pam-release-so.sh, invoked
# from a goreleaser post-build hook on linux-pam-build
# (docs/release-phase6-artifacts.md).
#
# aarch64 on an amd64 build host, so this image is only usable once
# tonistiigi/binfmt (or equivalent qemu-user-static registration) has
# registered the arm64 handler -- see the script for the check.
#
# Digest pinned in .goreleaser.yml next to linux-pam-build; keep the two in
# sync if it is ever re-pinned.
FROM --platform=linux/arm64 amazonlinux@sha256:af6e7c0a7d9abb123f2d27f12b1c2aceedfd80add1cc3109b93440ab900af4d2

RUN yum install -y pam-devel gcc make tar gzip \
    && yum clean all

ARG GO_VERSION
RUN test -n "$GO_VERSION" \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"
