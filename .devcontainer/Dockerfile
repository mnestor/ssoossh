FROM mcr.microsoft.com/devcontainers/go:dev-1.24
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

# COPY --from=1password/op:2 /usr/local/bin/op /usr/local/bin/op

# RUN mkdir -p -m 755 /etc/apt/keyrings \
#   && out=$(mktemp) && wget -nv -O$out https://cli.github.com/packages/githubcli-archive-keyring.gpg \
#   && cat $out | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null \
#   && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
#   && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
RUN apt-get update \
  && apt-get -y install \
  # gh \
  # vim \
  dnsutils \
  # golint \
  # gotestsum \
  git-crypt \
  docker \
  # && curl -Ls https://github.com/tmatilai/direnv-1password/raw/v1.0.0/1password.sh -o /usr/local/bin/1password.sh \ 
  # && chmod +x /usr/local/bin/1password.sh \
  # && curl -L -s -o goreleaser.deb \
  # $(curl -sL $GITHUBAPI$GORELEASER|jq -r '.assets[] |select(.name | endswith("arm64.deb")) .browser_download_url') \
  # && dpkg -i goreleaser.deb \
  # && curl -L -s -o gomarkdoc.tar.gz \
  # $(curl -sL $GITHUBAPI$GOMARKDOC|jq -r '.assets[] |select(.name | contains("linux_arm64.tar.gz")) .browser_download_url') \
  # && tar --strip-components=1 --wildcards -zxvf gomarkdoc.tar.gz -C /usr/bin "*/gomarkdoc" \
  # && curl -sLo svu.tgz `curl -s https://api.github.com/repos/caarlos0/svu/releases/latest |jq -r '.assets[] |select(.name | contains("_linux_arm64.tar.gz")) .browser_download_url'` \
  # && tar -zxvf svu.tgz -C /usr/bin svu \
  # && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/bin

