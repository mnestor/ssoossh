FROM mcr.microsoft.com/devcontainers/go:1.23
ARG CREATED
ARG COMMIT
ARG VERSION=local
ARG GITHUBAPI=https://api.github.com/repos/
ARG GORELEASER=goreleaser/goreleaser/releases/latest
ARG GOMARKDOC=princjef/gomarkdoc/releases/latest
LABEL \
  org.opencontainers.image.created=$CREATED \
  org.opencontainers.image.version=$VERSION \
  org.opencontainers.image.revision=$COMMIT \
  org.opencontainers.image.title="Go Dev container" \
  org.opencontainers.image.description="Go development container for Visual Studio Code Remote Containers development"
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH \
  CGO_ENABLED=0 \
  GO111MODULE=on

RUN apt-get update \
  && apt-get -y install \
  vim \
  # golint \
  gotestsum \
  git-crypt \
  docker \
  && curl -L -s -o goreleaser.deb \
  $(curl -sL $GITHUBAPI$GORELEASER|jq -r '.assets[] |select(.name | endswith("arm64.deb")) .browser_download_url') \
  && dpkg -i goreleaser.deb \
  && curl -L -s -o gomarkdoc.tar.gz \
  $(curl -sL $GITHUBAPI$GOMARKDOC|jq -r '.assets[] |select(.name | contains("linux_arm64.tar.gz")) .browser_download_url') \
  && tar --strip-components=1 --wildcards -zxvf gomarkdoc.tar.gz -C /usr/bin "*/gomarkdoc" \
  && curl -sLo svu.tgz `curl -s https://api.github.com/repos/caarlos0/svu/releases/latest |jq -r '.assets[] |select(.name | contains("_linux_arm64.tar.gz")) .browser_download_url'` \
  && tar -zxvf svu.tgz -C /usr/bin svu \
  && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/bin

