# Build environment for linking pam_ssoossh.so against the amd64 glibc
# floor (glibc 2.17). Used only by scripts/build-pam-release-so.sh, invoked
# from a goreleaser post-build hook on linux-pam-build
# (docs/release-phase6-artifacts.md). Not part of any developer's everyday
# build -- see the Makefile's `pam` target for that.
#
# Digest pinned in .goreleaser.yml next to linux-pam-build; keep the two in
# sync if it is ever re-pinned.
FROM centos@sha256:be65f488b7764ad3638f236b7b515b3678369a5124c47b8d32916d6487418ea4

# CentOS 7 is EOL: mirrorlist.centos.org no longer resolves to anything
# useful, so point yum at the vault archive instead.
RUN sed -i \
      -e 's|mirrorlist=|#mirrorlist=|g' \
      -e 's|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' \
      /etc/yum.repos.d/CentOS-Base.repo \
    && yum install -y pam-devel gcc make tar gzip \
    # this gets us git > 2.x
    && yum -y install https://packages.endpointdev.com/rhel/7/os/x86_64/endpoint-repo.x86_64.rpm \
    && yum -y install git \
    && yum clean all

ARG GO_VERSION
ARG GORELEASER_VERSION
RUN test -n "$GO_VERSION" \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz \
    && test -n "${GORELEASER_VERSION}" \
    && curl -fsSL "https://github.com/goreleaser/goreleaser/releases/download/${GORELEASER_VERSION}/goreleaser_Linux_x86_64.tar.gz" -o /tmp/gor.tar.gz \
    && tar -C /usr/local/bin -xzf /tmp/gor.tar.gz goreleaser \
    && rm /tmp/gor.tar.gz \
    && mkdir /workspace

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"

COPY entrypoint.sh /
ENTRYPOINT ["/entrypoint.sh"]
WORKDIR /workspace