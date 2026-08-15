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
	go test -coverprofile=.coverage/coverage-$(1).out ./$(1)/...
	grep -v -E -f exclude-from-coverage.txt .coverage/coverage-$(1).out > .coverage/coverage-$(1).filtered.out
	go tool cover -func=.coverage/coverage-$(1).filtered.out | tail -1
endef

.PHONY: all version frontend build binaries linux pam frontend-clean
.EXPORT_ALL_VARIABLES:

all: frontend binaries

# Build the web UI into server/frontend/dist, which server/frontend embeds
# via //go:embed all:dist/*. Nothing in dist/ is tracked, so this is a hard
# prerequisite of building or testing the Go side — without it the embed
# matches no files and the build fails. See docs/delivery-phase1-build-ci.md.
# CI=true keeps pnpm non-interactive: without it, an install that needs to
# purge node_modules aborts rather than prompting when there is no TTY.
frontend:
	cd frontend && CI=true pnpm install --frozen-lockfile && CI=true pnpm build

# Rebuilds the UI only when it is missing. Use `make frontend` to force one.
FRONTEND_DIST := server/frontend/dist/index.html

$(FRONTEND_DIST):
	$(MAKE) frontend

build: $(FRONTEND_DIST)
	go build ./...

linux:
	$(call BUILDIT,linux,amd64)

binaries:
	$(call BUILDALL)

# pam_ssoossh needs libpam headers and cgo, both installed by
# .devcontainer/Dockerfile (see docs/release-phase3-pam-build-env.md).
# CGO_ENABLED is set here rather than globally so the rest of the tree keeps
# building cgo-free.
pam:
	mkdir -p .build
	CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared -o .build/pam_ssoossh.so ./pam_ssoossh/

frontend-clean:
	rm -rf server/frontend/dist

.PHONY: openapi openapi-check
# docs/openapi.yaml is generated from the swag annotations on the handlers in
# server/controller (plus the envelope types in server/openapidoc). Edit those,
# not the YAML.
#
# The dir list matters: swag resolves types only from packages it was pointed
# at, and the general-info file (-g) has to live in the first one. --parseInternal
# is what lets it see internal/apitypes.
SWAG_DIRS := server/openapidoc,server/controller,server/bootstrap,server/webtypes,internal/apitypes,server/model

# swag always writes "swagger.yaml"; the rename is the only reason this is not
# a one-liner. Keeping the canonical name is worth it — every doc, comment, and
# rule in the repo points at docs/openapi.yaml.
openapi:
	go tool swag init -g openapidoc.go -d $(SWAG_DIRS) --v3.1 --parseInternal --parseDependency -o docs --ot yaml
	@mv docs/swagger.yaml docs/openapi.yaml

# Same shape as types-check: assert regenerating changes nothing. See the
# comment there for why this hashes rather than asking git.
openapi-check:
	@before=$$(sha256sum docs/openapi.yaml 2>/dev/null); \
	$(MAKE) --no-print-directory openapi >/dev/null; \
	after=$$(sha256sum docs/openapi.yaml 2>/dev/null); \
	if [ "$$before" != "$$after" ]; then \
		echo "docs/openapi.yaml is stale: a handler annotation or a wire type changed"; \
		echo "without the spec being regenerated."; \
		echo "Run 'make openapi' and commit the result."; \
		exit 1; \
	fi

.PHONY: types types-check
# Regenerate the frontend's wire types from the Go structs that produce them
# (see tygo.yaml). The output is committed so that pnpm check/test and an
# editor work without a Go toolchain; types-check is what CI runs to catch a
# commit that changed the Go side and forgot to regenerate.
types:
	go tool tygo generate

# Asserts that regenerating changes nothing, by hashing the output either
# side of a run. Deliberately not `git diff`: that ignores untracked files, so
# a never-committed generated file would pass while reporting nothing, and it
# reports a false failure when the correct output is merely staged rather than
# committed. Hashes answer the question actually being asked — is what is on
# disk what the Go source produces.
GENERATED_TYPES := frontend/src/lib/api/generated

types-check:
	@before=$$(find $(GENERATED_TYPES) -type f -exec sha256sum {} + 2>/dev/null | sort); \
	$(MAKE) --no-print-directory types >/dev/null; \
	after=$$(find $(GENERATED_TYPES) -type f -exec sha256sum {} + 2>/dev/null | sort); \
	if [ "$$before" != "$$after" ]; then \
		echo "$(GENERATED_TYPES) is stale: a Go wire type changed without the"; \
		echo "generated TypeScript being regenerated."; \
		echo "Run 'make types' and commit the result."; \
		exit 1; \
	fi

update-go-version:
	@echo "Must update .devcontainer/Dockerfile, go.mod"

act-ci:
	act --container-architecture linux/amd64 -s GITHUB_TOKEN --secret-file .secrets -P workflow_dispatch --container-daemon-socket=- -j lint

update:
	go get -u ./...
	go mod tidy

.PHONY: test test-server test-client test-pam test-internal test-e2e cover lint
# server/frontend embeds server/frontend/dist and nothing there is tracked,
# so the UI has to exist before the Go tests can compile at all.
test: $(FRONTEND_DIST) test-server test-client test-pam test-internal

test-server:
	$(call TESTCOMPONENT,server)

test-client:
	$(call TESTCOMPONENT,client)

test-pam:
	CGO_ENABLED=1 go test -tags=pam ./pam_ssoossh/...

test-internal:
	$(call TESTCOMPONENT,internal)

# The merge-gate end-to-end suite (docs/e2e-testing-plan.md): a real
# ssoosshd and ssoossh, a harness-provided OIDC IdP, a private ssh-agent,
# and a real sshd. Behind the `e2e` build tag, so it never runs as part of
# `make test`. Tier 3 modifies the host (creates and unlocks a dedicated
# local account, runs sshd as root via sudo) — see test/e2e/README.md.
test-e2e: $(FRONTEND_DIST)
	go test -tags=e2e -count=1 -timeout=10m ./test/e2e/...

cover: $(FRONTEND_DIST)
	go test -coverprofile=.coverage/coverage-all.out ./...
	grep -v -E -f exclude-from-coverage.txt .coverage/coverage-all.out > .coverage/coverage.out
	go tool cover -html=.coverage/coverage.out -o .coverage/coverage.html

lint: $(FRONTEND_DIST)
	golangci-lint run ./...

lint-server:
	golangci-lint run ./server/...

lint-client:
	golangci-lint run ./client/...

# CGO_ENABLED=1 is required here: golangci-lint's type-checker cannot see
# into a cgo file (pam.go, pam_ssoossh.go) without it, and with those files
# invisible every symbol only referenced from them reports as unused.
lint-pam:
	CGO_ENABLED=1 golangci-lint run --build-tags pam ./pam_ssoossh/...

lint-internal:
	golangci-lint run ./internal/...

version:
	@echo "Version: $(VERSION)"

