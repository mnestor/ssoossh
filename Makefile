-include .env
-include .env.local

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

LDFLAGS := -ldflags="${VAR_VERSION} ${VAR_COMMIT} ${VAR_DATE} ${VAR_BUILTBY}"
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

define TESTCOMPONENT
	mkdir -p .coverage
	go test -coverprofile=.coverage/coverage-$(1).out -tags=coverprofile ./$(1)/...
	grep -v -E -f exclude-from-coverage.txt .coverage/coverage-$(1).out > .coverage/coverage-$(1).filtered.out
	go tool cover -func=.coverage/coverage-$(1).filtered.out | tail -1
endef

.PHONY: all version $(MODULES) build build-server build-client
.EXPORT_ALL_VARIABLES:

all: $(MODULES)

linux:
	$(call BUILDIT,linux,amd64)

binaries:
	$(call BUILDALL)

pam:
	goreleaser build --clean --snapshot --id linux-pam-build

update-go-version:
	@echo "Must update .devcontainer/Dockerfile, go.mod"

act-ci:
	act --container-architecture linux/amd64 -s GITHUB_TOKEN --secret-file .secrets -P workflow_dispatch --container-daemon-socket=- -j golangci

pam:
	goreleaser build --clean --snapshot --id linux-pam-build

update:
	go get -u ./...
	go mod tidy

.PHONY: test test-server test-client test-pam test-internal cover

test: test-server test-client test-pam test-internal

test-server:
	$(call TESTCOMPONENT,server)

test-client:
	$(call TESTCOMPONENT,client)

test-pam:
	$(call TESTCOMPONENT,pam_ssoossh)

test-internal:
	$(call TESTCOMPONENT,internal)

cover:
	go test -coverprofile=.coverage/coverage-all.out -tags=coverprofile ./...
	grep -v -E -f exclude-from-coverage.txt .coverage/coverage-all.out > .coverage/coverage.out
	go tool cover -html=.coverage/coverage.out -o .coverage/coverage.html

lint:
	golangci-lint run ./...

lint-server:
	golangci-lint run ./server/...

lint-client:
	golangci-lint run ./client/...

lint-pam:
	golangci-lint run ./pam_ssoossh/...

lint-internal:
	golangci-lint run ./internal/...

version:
	@echo "Version: $(VERSION)"

