include .env

ifdef GITHUB_REF
VERSION ?= $(shell echo $(GITHUB_REF) | sed 's/^refs\/tags\///')
else
VERSION ?= $(shell [ -d .git ] && git describe --tags --always --dirty="-dev" || echo "Unknown")
endif

VERSION_PATH := github.com/mnestor/ssoossh/internal/version

DATE    := $(shell date -u '+%Y-%m-%d %H:%M UTC')
VERSION := $(shell echo $(VERSION) | sed 's/^v//')
COMMIT_HASH := $(shell git rev-parse HEAD)
DIRTY := $(shell git diff --exit-code >/dev/null && echo false || echo true)

VAR_VERSION := -X '$(VERSION_PATH).Version=$(VERSION)'
VAR_COMMIT := -X '$(VERSION_PATH).Commit=$(COMMIT_HASH)'
VAR_DATE := -X '$(VERSION_PATH).Date=$(DATE)'
VAR_BUILTBY := -X '$(VERSION_PATH).BuiltBy="Makefile"'
VAR_API := -X '$(VERSION_PATH).ApiPath=${API_PATH}

LDFLAGS := -ldflags="${VAR_VERSION} ${VAR_COMMIT} ${VAR_DATE} ${VAR_BUILTBY} ${VAR_API}"
GCFLAGS := -gcflags "all=-N -l"

# $(1) -- Go Operating System (e.g. linux, darwin, windows, etc.)
# $(2) -- Go Architecture (e.g. amd64, arm, arm64, etc.)
define BUILDIT
	echo "============================================================"
	echo "Building: $(BINARY_OUTPUT)$(strip $(1))-$(strip $(2))"
	RELEASE=false GOOS=$(strip $(1)) GOARCH=$(strip $(2)) goreleaser build --single-target --clean --snapshot=${DIRTY}
endef

define BUILDALL
	RELEASE=false goreleaser build --clean --snapshot=${DIRTY}
endef

.PHONY: all version $(MODULES) build build-server build-client
.EXPORT_ALL_VARIABLES:

all: $(MODULES)

linux:
	$(call BUILDIT,linux,amd64)

binaries:
	$(call BUILDALL)

update-go-version:
	@echo "Must update .devcontainer/Dockerfile, go.mod"

ci:
	golangci-lint run

act-ci:
	act --container-architecture linux/amd64 -s GITHUB_TOKEN --secret-file .secrets -P workflow_dispatch --container-daemon-socket=- -j golangci

tag-dev:
	@echo git tag -a 'v$(shell date -u '+%Y.%U.%u')-$(shell git rev-parse --short HEAD)-dev'

update:
	go get -u ./...
	go mod tidy

.PHONY: test test-server test-client test-pam cover

test: test-server test-client test-pam

test-server:
	go test -coverprofile=coverage-server.out -tags=coverprofile ./server/...
	grep -v -E -f exclude-from-coverage.txt coverage-server.out > coverage-server.filtered.out
	go tool cover -func=coverage-server.filtered.out | tail -1

test-client:
	go test -coverprofile=coverage-client.out -tags=coverprofile ./client/...

test-pam:
	go list ./pam_ssoossh/... >/dev/null 2>&1 && \
		go test -coverprofile=coverage-pam.out -tags=coverprofile ./pam_ssoossh/... || \
		echo "mode: set" > coverage-pam.out

cover: test
	echo "mode: set" > coverage.out
	tail -q -n +2 coverage-*.out >> coverage.out
	grep -v -E -f exclude-from-coverage.txt coverage.out > coverage.filtered.out
	mv coverage.filtered.out coverage.out
	go tool cover -html=coverage.out -o coverage.html

lint: lint-server lint-client lint-pam

lint-server:
	golangci-lint run ./server/...

lint-client:
	golangci-lint run ./client/...

lint-pam:
	golangci-lint run ./pam_ssoossh/...
	
version:
	@echo "Version: $(VERSION)"

pam:
	goreleaser build --clean --snapshot --id linux-pam-build
