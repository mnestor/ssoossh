FROM node:22-bookworm-slim AS claude-builder
RUN npm install -g @anthropic-ai/claude-code

FROM mcr.microsoft.com/devcontainers/go
ARG CREATED
ARG COMMIT
ARG VERSION=local
LABEL \
  org.opencontainers.image.created=$CREATED \
  org.opencontainers.image.version=$VERSION \
  org.opencontainers.image.revision=$COMMIT \
  org.opencontainers.image.title="Go Dev container" \
  org.opencontainers.image.description="Go development container for Visual Studio Code Remote Containers development"
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH \
  CGO_ENABLED=0 \
  GO111MODULE=on \
  GOSUMDB=off

COPY --from=1password/op:2 /usr/local/bin/op /usr/local/bin/op
COPY --from=claude-builder /usr/local/lib/node_modules/@anthropic-ai /usr/local/lib/node_modules/@anthropic-ai
COPY --from=claude-builder /usr/local/bin/claude /usr/local/bin/claude

RUN install -d -m 0755 /etc/apt/keyrings \
  && echo "deb [trusted=yes] https://repo.goreleaser.com/apt/ /" > /etc/apt/sources.list.d/goreleaser.list \

  # arm64 is a foreign architecture on this amd64 base image; it must be
  # registered before any :arm64 package resolves, which is what the
  # cross libpam headers below need.
  && dpkg --add-architecture arm64 \
  && apt-get update \
  && apt-get -y install \
  dnsutils \
  goreleaser \
  jq \
  ripgrep \
  git-crypt \
  yamllint \
  # pam_ssoossh (pam_ssoossh/, `pam` build tag): native libpam headers for
  # the amd64 build, plus arm64 cross headers, cross libc, and cross gcc
  # for `goreleaser build` and `make pam`'s arm64 target. See
  # docs/release-phase3-pam-build-env.md.
  libpam0g-dev \
  libpam0g-dev:arm64 \
  libc6-dev-arm64-cross \
  gcc-aarch64-linux-gnu \

  && curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(go env GOPATH)/bin

USER vscode

RUN go install github.com/princjef/gomarkdoc/cmd/gomarkdoc@latest \
  && go install github.com/caarlos0/svu/v3@latest \
  && go install golang.org/x/lint/golint@latest \
  && go install gotest.tools/gotestsum@latest \
  && go install github.com/rhysd/actionlint/cmd/actionlint@latest