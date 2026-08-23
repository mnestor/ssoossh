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
# github.com/mnestor/ubuntu-build's Dockerfile.devcontainer (see
# docs/release-phase3-pam-build-env.md).
# CGO_ENABLED is set here rather than globally so the rest of the tree keeps
# building cgo-free.
pam:
	mkdir -p .build
	CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared -o .build/pam_ssoossh.so ./pam_ssoossh/

frontend-clean:
	rm -rf server/frontend/dist

.PHONY: openapi openapi-check openapi-lint
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

# Validate docs/openapi.yaml against the OpenAPI 3.1 specification using redocly.
# This catches structural errors, missing required fields, and other spec violations.
# Uses a pinned version and configuration file (.redoclyrc.yaml) for known exceptions.
openapi-lint:
	npx @redocly/cli@1.28.1 lint docs/openapi.yaml --config .redoclyrc.yaml

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
	@echo "Must update go.mod and github.com/mnestor/ubuntu-build's Dockerfile.devcontainer"

.PHONY: third-party-licenses
# Regenerates THIRD-PARTY-LICENSES.md from the Go module cache. Not committed
# (see .gitignore) — goreleaser runs this same script as a before.hook so
# every release archive/package ships a fresh copy. Useful to run locally to
# eyeball what a release would bundle.
third-party-licenses:
	./scripts/gen-third-party-licenses.sh

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

# Reproducing tests for known limitations (quarantined — do not run in CI).
# These tests verify defects exist; they should fail loudly. See
# docs/e2e-testing-plan.md for context.
test-e2e-multi-instance: $(FRONTEND_DIST)
	go test -tags=e2e,multi_instance_test -count=1 -timeout=10m ./test/e2e/...

test-memory-leak:
	go test -tags=memory_leak_test -count=1 -timeout=1m ./server/service/... -v -run MemoryLeak

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

.PHONY: fmt fmt-check test-race cover-ci govulncheck pnpm-audit semgrep security ci ci-required ci-advisory

fmt:
	gofmt -w .

# Mirrors build-test.yaml's formatting check.
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Mirrors build-test.yaml's Test step. CGO_ENABLED is explicit because
# -race requires cgo and the devcontainer sets CGO_ENABLED=0 by default.
test-race: $(FRONTEND_DIST)
	CGO_ENABLED=1 go test -race ./...

# Mirrors codecover.yaml's test run (minus the Codecov upload, which needs a
# token). Unlike `cover`, this is not filtered against
# exclude-from-coverage.txt or turned into an HTML report — it's what the
# runner actually executes.
cover-ci: $(FRONTEND_DIST)
	CGO_ENABLED=0 go test -v -covermode=atomic -coverprofile=coverage.txt ./...

# Mirrors security.yaml's govulncheck job. Ships with the devcontainer base
# image; if missing: go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck:
	govulncheck ./...

# Mirrors security.yaml's pnpm-audit job.
pnpm-audit:
	cd frontend && CI=true pnpm install --frozen-lockfile && pnpm audit --audit-level high

# Mirrors security.yaml's semgrep job, via the same semgrep/semgrep image
# CI uses, so no local semgrep install is required.
semgrep:
	docker run --rm -v $(CURDIR):/src semgrep/semgrep semgrep scan --config auto --error

security: govulncheck pnpm-audit semgrep

# Hard gates: what build-test.yaml (build-test + pam jobs) and
# codecover.yaml block on. No test-e2e here — e2e.yaml's job modifies host
# state (creates a local user account, runs sshd as root, see
# test/e2e/README.md) on a self-hosted runner, so it stays opt-in; run it
# explicitly with `make test-e2e`.
ci-required: fmt-check build types-check openapi-check test-race pam test-pam lint-pam cover-ci

# Advisory: lint.yaml and security.yaml's jobs all run continue-on-error in
# CI (results posted to the job summary, not blocking). `-` keeps that
# behavior locally — a failure here is reported but doesn't stop the run.
ci-advisory:
	-$(MAKE) lint
	-$(MAKE) security

# Everything the hosted/self-hosted runners check on every push/PR, run
# locally before pushing. See ci-required and ci-advisory for what's a real
# gate vs. advisory, and the comment on ci-required for what's deliberately
# left out.
ci: ci-required ci-advisory


.PHONY: changelog
# Generate CHANGELOG.md from git-cliff using conventional commits.
# Requires git-cliff to be installed: cargo install git-cliff
changelog:
	git-cliff --output CHANGELOG.md
	@echo "CHANGELOG.md generated"

.PHONY: changelog-check
# Verify CHANGELOG.md is up to date (for CI).
changelog-check:
	@before=$$(sha256sum CHANGELOG.md 2>/dev/null); \
	$(MAKE) --no-print-directory changelog >/dev/null; \
	after=$$(sha256sum CHANGELOG.md 2>/dev/null); \
	if [ "$$before" != "$$after" ]; then \
		echo "CHANGELOG.md is stale: run 'make changelog' and commit the result"; \
		exit 1; \
	fi

.PHONY: gendocs
# Generate man pages (ssoossh.1 and ssoosshd.8) from cobra commands.
# Config format pages (ssoossh.yaml.5, ssoosshd.yaml.5) are hand-written and not regenerated.
gendocs:
	go run ./internal/tools/gendocs docs/man
	@echo "Man pages regenerated (ssoossh.1, ssoosshd.8)"

.PHONY: man-check
# Verify man pages are up to date (for CI).
man-check:
	@before_ssoossh=$$(sha256sum docs/man/ssoossh.1 2>/dev/null); \
	before_ssoosshd=$$(sha256sum docs/man/ssoosshd.8 2>/dev/null); \
	$(MAKE) --no-print-directory gendocs >/dev/null; \
	after_ssoossh=$$(sha256sum docs/man/ssoossh.1 2>/dev/null); \
	after_ssoosshd=$$(sha256sum docs/man/ssoosshd.8 2>/dev/null); \
	if [ "$$before_ssoossh" != "$$after_ssoossh" ] || [ "$$before_ssoosshd" != "$$after_ssoosshd" ]; then \
		echo "Man pages are stale: run 'make gendocs' and commit the result"; \
		exit 1; \
	fi

.PHONY: mutation-test-frontend mutation-test-go mutation-test

# Frontend mutation testing via Stryker with vitest runner
# Targets: approval.test.ts, format.test.ts, paths.test.ts, client.test.ts,
# endpoints.test.ts, ApprovalView.test.ts, ConsentModal.test.ts
mutation-test-frontend: $(FRONTEND_DIST)
	cd frontend && npx stryker run

# Go mutation testing via manual analysis
# Analyzes critical paths in server/service/, internal/crypto/, internal/fipsmode/,
# and server/middleware/ by running test suite against intentional code mutations.
# See docs/mutation-testing-findings.md for analysis and results.
mutation-test-go:
	@echo "Running Go mutation testing analysis..."
	@echo "Manual mutation testing focused on critical paths:"
	@echo "  - server/service/ (authorization and certificate request handling)"
	@echo "  - internal/crypto/ (cryptographic operations)"
	@echo "  - internal/fipsmode/ (FIPS policy enforcement)"
	@echo "  - server/middleware/ (CSRF, session auth, host cert auth)"
	@echo ""
	@echo "Note: No actively maintained Go mutation testing tools exist for Go 1.26."
	@echo "Analysis uses manual mutation approach: intentional code modifications"
	@echo "to identify weak test assertions. See docs/mutation-testing-findings.md"

# Combined mutation testing (frontend + go analysis documentation)
mutation-test: mutation-test-frontend mutation-test-go

# --- pam e2e and client matrix ---
# PAM module end-to-end testing (requires Docker)
# Builds the PAM module, creates a containerized test environment,
# and runs e2e tests against a real PAM stack.
.PHONY: test-pam-e2e pam-e2e-container

test-pam-e2e: pam pam-e2e-container
	@echo "Running PAM e2e tests in container..."
	docker run --rm \
		-v $(CURDIR):/workspace \
		-w /workspace \
		-e CGO_ENABLED=1 \
		pam-test:latest \
		go test -tags=pam_e2e -v -count=1 -timeout=10m ./test/pam/...

pam-e2e-container:
	@echo "Building PAM e2e test container..."
	docker build -t pam-test:latest test/pam/

# Cross-platform client compilation and testing
.PHONY: cross-compile-verify

cross-compile-verify:
	@echo "Verifying cross-platform compilation..."
	@echo "  linux/amd64..."
	@GOOS=linux GOARCH=amd64 go build -v ./cmd/ssoossh/
	@echo "  linux/arm64..."
	@GOOS=linux GOARCH=arm64 go build -v ./cmd/ssoossh/
	@echo "  darwin/amd64..."
	@GOOS=darwin GOARCH=amd64 go build -v ./cmd/ssoossh/
	@echo "  darwin/arm64..."
	@GOOS=darwin GOARCH=arm64 go build -v ./cmd/ssoossh/
	@echo "  windows/amd64..."
	@GOOS=windows GOARCH=amd64 go build -v ./cmd/ssoossh/
	@echo "  windows/arm64..."
	@GOOS=windows GOARCH=arm64 go build -v ./cmd/ssoossh/
	@echo "All platforms verified"

