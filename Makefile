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

# Wide on purpose, and the blast radius is worth knowing about: this exports
# every variable above into every recipe's environment, and also everything
# picked up from the `-include .env` / `.env.local` at the top. That is how a
# local NFPM_PASSPHRASE or QUILL_* reaches goreleaser without each recipe
# naming it. Removing it would silently break a local signed build.
.EXPORT_ALL_VARIABLES:

# $(1) -- Go Operating System (e.g. linux, darwin, windows, etc.)
# $(2) -- Go Architecture (e.g. amd64, arm, arm64, etc.)
define BUILDIT
	echo "============================================================"
	echo "Building: $(strip $(1))-$(strip $(2))"
	RELEASE=false GOOS=$(strip $(1)) GOARCH=$(strip $(2)) goreleaser build --single-target --clean --snapshot=${DIRTY}
endef

define BUILDALL
	RELEASE=false goreleaser build --clean --snapshot=${DIRTY}
endef

# $(1) -- component directory (server, client, internal)
# $(2) -- CGO_ENABLED for the test run. Inside the recipe rather than in
# front of $(call ...) at the call site: each line of a define is its own
# shell, so a prefix there would only reach the mkdir.
define TESTCOMPONENT
	mkdir -p .coverage
	CGO_ENABLED=$(2) go test -coverprofile=.coverage/coverage-$(1).out ./$(1)/...
	go tool cover -func=.coverage/coverage-$(1).out | tail -1
endef

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make <target>\n"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)
	@echo ""

##@ Before you open a PR

.PHONY: pre-pr
# The one target a contributor needs. Order matters: the two autofix steps
# run first so the checks after them see already-conforming code, then the
# generated-artifact gates, then lint and the unit suite. See CONTRIBUTING.md.
pre-pr: fmt lint-fix check-generated ci-required ## Format, autofix, then run every merge gate CI runs

.PHONY: verify
# The edit-loop subset: the checks that compile or typecheck Go, and nothing
# that needs a container, the host, or a coverage run.
#
# `lint-tagged` and `lint-cross` are the reason this target exists rather
# than people running `lint` and `test` by hand. `lint` passes no build tags
# and `test` does not build the tagged suites, so a suite behind `e2e`,
# `resilience`, `load`, `dbparity`, `softhsm` or `natsintegration` can fail
# to compile outright while both report success. `lint` also runs with the
# host GOOS, so nothing here sees the Windows or macOS build at all. Any
# hand-assembled subset that omits either will believe it has coverage it
# does not have.
#
# This is NOT a substitute for `pre-pr`, which is the merge gate. Verify with
# `pre-pr` before opening a PR -- never with a subset. See CONTRIBUTING.md.
verify: lint lint-tagged lint-cross test check-generated ## Fast subset for the edit loop -- still compiles the tagged suites

.PHONY: worktree
# Creating an agent worktree by hand trips over the same three things every
# time: /workspace is root-owned so git cannot make the directory, a failed
# attempt leaves the branch behind so the retry fails differently, and the
# new worktree has no frontend build so ~15 server/bootstrap tests fail on
# a missing index.html. See docs/dev/parallel-agent-workflow.md.
worktree: ## Create a ready-to-use agent worktree: make worktree NAME=<name> [BASE=main] [WORKTREE_BRANCH=...]
	@test -n "$(NAME)" || { echo "usage: make worktree NAME=<name> [BASE=main] [WORKTREE_BRANCH=<branch>]" >&2; exit 2; }
	WORKTREE_BRANCH="$(WORKTREE_BRANCH)" ./scripts/new-worktree.sh "$(NAME)" "$(or $(BASE),main)"

##@ Build

.PHONY: all frontend build binaries linux pam frontend-clean
all: frontend binaries

# Build the web UI into server/frontend/dist, which server/frontend embeds
# via //go:embed all:dist/*.
#
# Note this is NOT required just to compile: the tracked dist/.gitignore is
# enough for the embed pattern to match, so `go build`, `go vet`, and
# golangci-lint all work on a clean checkout. It is required to get a
# server binary that actually serves a UI, and for any test that exercises
# one.
#
# CI=true keeps pnpm non-interactive: without it, an install that needs to
# purge node_modules aborts rather than prompting when there is no TTY.
frontend: ## Build the web UI into server/frontend/dist
	cd frontend && CI=true pnpm install --frozen-lockfile && CI=true pnpm build

# The bundle, and everything that goes into it. The sources are listed as
# real prerequisites rather than the bundle being a bare "build it if it is
# missing" target: the server embeds this bundle, so a stale one means a
# browser test asserts against markup from whenever the UI was last built.
# That failure presents as a selector timeout, which is indistinguishable
# from a wrong selector -- see docs/dev/agent-workflow-friction.md.
# Use `make frontend` to force a rebuild regardless.
FRONTEND_DIST := server/frontend/dist/index.html
FRONTEND_SRC := $(shell find frontend/src frontend/static -type f 2>/dev/null) \
	frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml \
	frontend/svelte.config.js frontend/vite.config.ts frontend/tsconfig.json

$(FRONTEND_DIST): $(FRONTEND_SRC)
	$(MAKE) frontend

# CGO_ENABLED=1 because server/signer's HSM key source reaches libpkcs11
# through crypto11: with cgo off the server packages do not build at all.
build: $(FRONTEND_DIST) ## Build all Go packages
	CGO_ENABLED=1 go build ./...

linux: ## Snapshot build for linux/amd64 only
	$(call BUILDIT,linux,amd64)

binaries: ## Snapshot build for every release target
	$(call BUILDALL)

# pam_ssoossh needs libpam headers and cgo, both installed by
# this repo's .github/docker/Dockerfile.devcontainer. On a bare host,
# run scripts/build-env-for-pam.sh first.
# CGO_ENABLED is set here rather than globally so the rest of the tree keeps
# building cgo-free.
pam: ## Build pam_ssoossh.so (cgo, needs libpam headers)
	mkdir -p .build
	CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared -o .build/pam_ssoossh.so ./pam_ssoossh/

frontend-clean: ## Remove the built web UI
	rm -rf server/frontend/dist

##@ Test

.PHONY: test test-server test-client test-pam test-internal test-race cover cover-ci frontend-test
# server/frontend embeds server/frontend/dist. The unit suite includes tests
# that assert on real UI assets, so the UI has to exist first.
test: $(FRONTEND_DIST) test-server test-client test-pam test-internal ## Unit tests per component, with coverage

# server and internal need cgo for the same reason `build` does: the HSM
# key source binds libpkcs11. client has no such dependency.
test-server:
	$(call TESTCOMPONENT,server,1)

test-client:
	$(call TESTCOMPONENT,client,0)

test-pam:
	CGO_ENABLED=1 go test -tags=pam ./pam_ssoossh/...

test-internal:
	$(call TESTCOMPONENT,internal,1)

# CGO_ENABLED is explicit because -race requires cgo and the devcontainer
# sets CGO_ENABLED=0 by default.
test-race: $(FRONTEND_DIST) ## Unit tests under -race
	CGO_ENABLED=1 go test -race ./...

# Unfiltered on purpose: an uncoverable block carries a "not covered:"
# comment at the code instead of a line range in a side file, so the number
# here is the same one CI and Codecov report.
cover: $(FRONTEND_DIST) ## Coverage HTML report at .coverage/coverage.html
	CGO_ENABLED=1 go test -coverprofile=.coverage/coverage.out ./...
	go tool cover -html=.coverage/coverage.out -o .coverage/coverage.html

# Mirrors codecover.yaml's test run (minus the Codecov upload, which needs a
# token) -- it is what the runner actually executes.
#
# Two runs, because ./... cannot see pam_ssoossh: every file there is behind
# //go:build pam, so the first run compiles the package to nothing and PAM
# contributes neither numerator nor denominator. Codecov takes both profiles;
# they cover disjoint packages, so nothing is double counted.
cover-ci: $(FRONTEND_DIST) ## Coverage exactly as codecover.yaml runs it
	CGO_ENABLED=1 go test -v -covermode=atomic -coverprofile=coverage.txt ./...
	CGO_ENABLED=1 go test -v -tags=pam -covermode=atomic -coverprofile=coverage-pam.txt ./pam_ssoossh/...

# The ratchet. Coverage regressions are the one finding in the audit that
# was about direction rather than position: client/cmd fell 88.7% -> 76.6%
# over a fortnight with nothing deleted, just new code landing
# under-tested, and no gate noticed. Per package rather than one module
# number, because the module total moves for unrelated reasons and hides
# exactly that.
.PHONY: cover-floors
cover-floors: $(FRONTEND_DIST) ## Fail if any package dropped below its floor in .coverage-floors
	./scripts/check-coverage-floors.sh

frontend-test: ## Frontend unit and a11y tests (vitest)
	cd frontend && CI=true pnpm install --frozen-lockfile && pnpm test

##@ Test (tagged suites, not part of `make test`)

.PHONY: test-e2e test-memory-leak test-resilience test-load test-migration test-hsm
# The HSM key source against a real PKCS#11 token. Behind the `softhsm` tag
# so `make test` never needs softhsm2 installed; CI installs softhsm2 and
# opensc for it (see the runner image).
test-hsm: ## HSM key source tests against softhsm2 (needs softhsm2 + opensc)
	CGO_ENABLED=1 go test -tags=softhsm ./server/signer/ -run TestHSMKeySource -v

# The merge-gate end-to-end suite (docs/dev/e2e-testing-plan.md): a real
# ssoosshd and ssoossh, a harness-provided OIDC IdP, a private ssh-agent,
# and a real sshd. Behind the `e2e` build tag, so it never runs as part of
# `make test`. Tier 3 modifies the host (creates and unlocks a dedicated
# local account, runs sshd as root via sudo) -- see test/e2e/README.md.
# CGO_ENABLED=1: the e2e test package itself is cgo-free, but the harness
# builds ssoosshd with the inherited environment, and that build needs cgo
# (crypto11 -> libpkcs11) like every other server build.
#
# Serialised with flock. Worktrees isolate the filesystem, not the host, and
# this suite touches host state: a local account, sshd under sudo, PAM
# service files, container ports. docs/dev/parallel-agent-workflow.md has
# always said "one run at a time" -- this makes that true rather than
# merely written down, since a second run otherwise interferes silently
# instead of failing. Concurrent invocations wait rather than error, so a
# second agent's run is delayed, never corrupted. Set E2E_LOCK= to opt out,
# or call test-e2e-unlocked directly. CI is unaffected either way: e2e.yaml
# invokes gotestsum on ./test/e2e/... itself and never goes through here.
E2E_LOCK ?= /tmp/ssoossh-e2e.lock

test-e2e: $(FRONTEND_DIST) ## End-to-end suite (modifies host state, read test/e2e/README.md first)
	@if [ -n "$(E2E_LOCK)" ] && command -v flock >/dev/null 2>&1; then \
		echo "waiting for the e2e lock ($(E2E_LOCK)) if another run holds it"; \
		flock "$(E2E_LOCK)" $(MAKE) test-e2e-unlocked; \
	else \
		$(MAKE) test-e2e-unlocked; \
	fi

.PHONY: test-e2e-unlocked
test-e2e-unlocked: ## test-e2e without the serialising lock (for a deliberate parallel run)
	CGO_ENABLED=1 go test -tags=e2e -count=1 -timeout=10m ./test/e2e/...

# Reproducing tests for known limitations (quarantined -- do not run in CI).
# These tests verify defects exist; they should fail loudly. The
# multi-instance repro that used to live here is gone: NATS made the
# topology real, so those tests assert the working behaviour and run in the
# normal e2e suite (and in CI's multi-signer job).
test-memory-leak: ## Memory leak repro tests
	CGO_ENABLED=1 go test -tags=memory_leak_test -count=1 -timeout=1m ./server/service/... -v -run MemoryLeak

# Mirrors resilience.yaml's resilience job.
test-resilience: $(FRONTEND_DIST) ## Resilience suite (shutdown, db loss, oidc loss)
	CGO_ENABLED=1 go test -tags=resilience -race -count=1 -timeout=5m ./test/resilience/...

# Mirrors resilience.yaml's load job, which runs weekly rather than per-PR.
test-load: $(FRONTEND_DIST) ## Load, soak, and concurrency suite (slow)
	CGO_ENABLED=1 go test -tags=load -race -count=1 -timeout=10m ./test/load/...

# SQLite/Postgres schema parity. Not wired into any workflow: the e2e tier-1
# matrix already runs both backends end to end, so this is the targeted
# version to reach for when a migration is what changed.
test-migration: ## SQLite/Postgres migration parity checks
	go test -tags=dbparity -count=1 ./test/migration/...

##@ Format and lint

.PHONY: fmt fmt-check lint-fix lint lint-cross lint-server lint-client lint-pam lint-internal
.PHONY: frontend-lint frontend-check actionlint check-gitignore
# `go list ./...` rather than `.`, and it matters: a git worktree checked out
# under .claude/worktrees/ is a nested module, so plain `gofmt -w .` would
# walk into someone else's branch and reformat it. `go list` skips nested
# modules and never returns the repo root, so this only ever touches this
# module's own packages. Computed in the recipe, not with $(shell), so
# `make help` does not pay for it.
fmt: ## Format Go and frontend sources in place
	gofmt -w $$(go list -f '{{.Dir}}' ./...)
	cd frontend && pnpm format

fmt-check: ## Fail if any Go file is not gofmt-clean
	@unformatted=$$(gofmt -l $$(go list -f '{{.Dir}}' ./...)); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Run this before `lint`. Several enabled linters are mechanically fixable
# -- godot (comment full stops), the gofmt/goimports formatters, and the
# interface{} -> any rewrite -- and there is no pre-commit hook in this repo
# to catch them, so without this they fail the merge gate instead.
# CGO_ENABLED=1 for the same reason lint-pam needs it: server/signer's HSM
# key source imports crypto11, which golangci-lint's type-checker cannot
# resolve without cgo.
lint-fix: ## Auto-fix every lint finding that golangci-lint can fix
	CGO_ENABLED=1 golangci-lint run --fix ./...
	# Second pass for pam_ssoossh: without the tag and cgo, golangci-lint
	# cannot see those files at all, so the first pass silently skips them.
	CGO_ENABLED=1 golangci-lint run --fix --build-tags pam ./pam_ssoossh/...

lint: ## golangci-lint over the whole module (merge gate)
	CGO_ENABLED=1 golangci-lint run ./...

# `lint` above passes no build tags, so every file behind e2e, resilience,
# load, dbparity or softhsm is invisible to it -- which is most of test/.
# Only lint-pam passed a tag before this, and only for PAM. Running it over
# the tagged suites for the first time surfaced findings that had been
# accumulating unseen in the one part of the tree nobody linted.
#
# One invocation per tag set rather than a single combined one: the tags
# select mutually exclusive views of the tree in places, so a combined run
# does not typecheck.
LINT_TAGGED := e2e resilience load dbparity softhsm natsintegration

.PHONY: lint-tagged
lint-tagged: ## golangci-lint over the build-tagged suites lint(1) cannot see
	@for tag in $(LINT_TAGGED); do \
		echo "golangci-lint --build-tags=$$tag"; \
		CGO_ENABLED=1 golangci-lint run --build-tags=$$tag ./... || exit 1; \
	done

# `lint` above runs with the host GOOS, so every file behind a `windows` or
# `darwin` constraint is invisible to it and findings accumulated in the one
# part of the tree that only ever runs on somebody else's machine. A G115
# integer-overflow bug sat in client/config/policy_windows.go this way, on a
# value an administrator sets through Group Policy.
#
# The scope is the same tree client-matrix.yaml tests, for the same reason:
# those are the only packages where GOOS changes what gets compiled. `./...`
# is not usable here -- server/signer imports crypto11, whose type
# information needs cgo, and there is no cross-compiling cgo toolchain.
#
# One GOARCH per GOOS is enough. The shipped amd64 and arm64 targets are
# both 64-bit, so they differ in nothing golangci-lint can see.
LINT_CROSS_PACKAGES ?= ./client/... ./internal/crypto/ssh/agent/... ./internal/fileperm/...
LINT_CROSS_GOOS ?= windows darwin

.PHONY: lint-cross
lint-cross: ## golangci-lint the Windows and macOS builds lint(1) cannot see
	@for goos in $(LINT_CROSS_GOOS); do \
		echo "golangci-lint GOOS=$$goos"; \
		GOOS=$$goos GOARCH=amd64 CGO_ENABLED=0 golangci-lint run $(LINT_CROSS_PACKAGES) || exit 1; \
	done

lint-server:
	CGO_ENABLED=1 golangci-lint run ./server/...

lint-client:
	golangci-lint run ./client/...

# CGO_ENABLED=1 is required here: golangci-lint's type-checker cannot see
# into a cgo file (pam.go, pam_ssoossh.go) without it, and with those files
# invisible every symbol only referenced from them reports as unused.
lint-pam: ## golangci-lint over pam_ssoossh (needs cgo)
	CGO_ENABLED=1 golangci-lint run --build-tags pam ./pam_ssoossh/...

lint-internal:
	golangci-lint run ./internal/...

# prettier --check plus eslint. Mirrors lint.yaml's frontend job.
frontend-lint: ## prettier --check and eslint over the frontend
	cd frontend && CI=true pnpm install --frozen-lockfile && pnpm lint

# svelte-check. The only thing that typechecks hand-written Svelte and
# TypeScript; types-check only covers the generated wire types.
frontend-check: ## svelte-check the frontend against tsconfig.json
	cd frontend && CI=true pnpm install --frozen-lockfile && pnpm check

# Mirrors lint.yaml's workflows job.
# Prefers the actionlint the build image installs (see
# .github/docker/Dockerfile.runner, ACTIONLINT_VERSION); `go run` otherwise,
# which fetches the tool and a matching Go toolchain every time. Keep the
# `go run` pin in step with the image's.
ACTIONLINT ?= $(shell command -v actionlint 2>/dev/null || echo "go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12")

actionlint: ## Lint the GitHub Actions workflow files
	$(ACTIONLINT)

# Mirrors lint.yaml's gitignore step: no tracked file may match an ignore
# rule, and no source file may be ignored. Cheap enough to run any time you
# touch a .gitignore.
check-gitignore: ## Assert the .gitignore invariants hold
	@scripts/check-gitignore.sh

##@ Generated artifacts

.PHONY: check-generated types types-check openapi openapi-check openapi-lint gendocs man-check
.PHONY: third-party-licenses
# Every "is the committed output still what the source produces" gate, in
# one place. All four are merge gates.
check-generated: types-check openapi-check openapi-lint man-check ## Assert every generated artifact is current

# Regenerate the frontend's wire types from the Go structs that produce them
# (see tygo.yaml). The output is committed so that pnpm check/test and an
# editor work without a Go toolchain; types-check is what CI runs to catch a
# commit that changed the Go side and forgot to regenerate.
types: ## Regenerate frontend wire types from Go structs
	go tool tygo generate

# Asserts that regenerating changes nothing, by hashing the output either
# side of a run. Deliberately not `git diff`: that ignores untracked files, so
# a never-committed generated file would pass while reporting nothing, and it
# reports a false failure when the correct output is merely staged rather than
# committed. Hashes answer the question actually being asked -- is what is on
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

# docs/openapi.yaml is generated from the swag annotations on the handlers in
# server/controller (plus the envelope types in server/openapidoc). Edit those,
# not the YAML.
#
# The dir list matters: swag resolves types only from packages it was pointed
# at, and the general-info file (-g) has to live in the first one. --parseInternal
# is what lets it see internal/apitypes.
SWAG_DIRS := server/openapidoc,server/controller,server/bootstrap,server/webtypes,internal/apitypes,server/model

# swag always writes "swagger.yaml"; the rename is the only reason this is not
# a one-liner. Keeping the canonical name is worth it -- every doc, comment, and
# rule in the repo points at docs/openapi.yaml.
openapi: ## Regenerate docs/openapi.yaml from swag annotations
	go tool swag init -g openapidoc.go -d $(SWAG_DIRS) --v3.1 --parseInternal --parseDependency -o docs --ot yaml
	@mv docs/swagger.yaml docs/openapi.yaml

# Same shape as types-check: assert regenerating changes nothing.
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
# REDOCLY prefers a redocly already on PATH -- the build image installs a
# pinned one (see .github/docker/Dockerfile.runner, REDOCLY_VERSION) so CI
# does not fetch it per run -- and falls back to npx for a developer who has
# not got it. Keep the npx pin in step with the image's.
REDOCLY ?= $(shell command -v redocly 2>/dev/null || echo "npx @redocly/cli@1.28.1")

openapi-lint:
	$(REDOCLY) lint docs/openapi.yaml --config .redoclyrc.yaml

# Generate man pages (ssoossh.1 and ssoosshd.8) from cobra commands.
# Config format pages (ssoossh.yaml.5, ssoosshd.yaml.5) are hand-written and
# not regenerated.
# CGO_ENABLED=1 for the same reason `build` needs it: gendocs imports the
# server command tree, which reaches the HSM key source's libpkcs11 binding.
gendocs: ## Regenerate man pages from the cobra commands
	CGO_ENABLED=1 go run ./internal/tools/gendocs docs/man
	@echo "Man pages regenerated (ssoossh.1, ssoosshd.8)"

# The pages gendocs produces: the two roots plus one per cobra subcommand.
# The glob matters. Hashing only ssoossh.1 and ssoosshd.8 (as this did
# originally) misses the subcommand pages entirely, so adding a cobra
# subcommand would produce a new page that nothing told you to commit.
#
# ssoossh*.1 rather than the literal ssoossh.1 for the same reason: the
# client page is now generated from the client's real command tree, so it
# produces a page per subcommand (18 of them) exactly as the server side
# always did. Pinning the literal name would have left seventeen of those
# ungated — the hole this whole change closes, reopened one level down.
#
# pam_ssoossh.8 and the .5 config-format pages are hand-written and are
# deliberately outside this set; a gendocs run leaves them byte-identical.
GENERATED_MAN := docs/man/ssoossh*.1 docs/man/ssoosshd*.8

man-check:
	@before=$$(sha256sum $(GENERATED_MAN) 2>/dev/null | sort); \
	$(MAKE) --no-print-directory gendocs >/dev/null; \
	after=$$(sha256sum $(GENERATED_MAN) 2>/dev/null | sort); \
	if [ "$$before" != "$$after" ]; then \
		echo "Man pages are stale: a cobra command's name, description, or"; \
		echo "subcommand set changed without the pages being regenerated."; \
		echo "Run 'make gendocs' and commit the result (including any new page)."; \
		exit 1; \
	fi

# Regenerates THIRD-PARTY-LICENSES.md from the Go module cache. Not committed
# (see .gitignore) -- goreleaser runs this same script as a before.hook so
# every release archive/package ships a fresh copy. Useful to run locally to
# eyeball what a release would bundle.
third-party-licenses: ## Regenerate THIRD-PARTY-LICENSES.md
	./scripts/gen-third-party-licenses.sh

##@ Security

.PHONY: govulncheck pnpm-audit semgrep security
# Mirrors security.yaml's govulncheck job (advisory there). Ships with the
# devcontainer base image; if missing:
# go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck: ## Scan Go dependencies for known vulnerabilities
	govulncheck ./...

# Mirrors security.yaml's pnpm-audit job (advisory there).
pnpm-audit: ## Audit frontend dependencies
	cd frontend && CI=true pnpm install --frozen-lockfile && pnpm audit --audit-level high

# Mirrors security.yaml's semgrep job, which IS a merge gate: same pinned
# image, same rule packs, same frontend-only scope. The Go side is covered
# by gosec inside golangci-lint, so pointing semgrep at it too would just
# re-report the same findings in a second tool.
# Delegates to scripts/semgrep.sh, which ships the sources into the
# container with `docker cp` rather than a bind mount. A bind mount resolves
# on the HOST daemon, so it silently comes up empty from any worktree other
# than the one the devcontainer was started in -- see the script's header.
semgrep: ## Semgrep scan of the frontend (merge gate)
	./scripts/semgrep.sh frontend/src

security: govulncheck pnpm-audit semgrep ## Run every security scanner

##@ CI mirrors

.PHONY: ci ci-required ci-advisory
# Every check that can fail a PR, in roughly the order CI reaches it.
# Deliberately absent: test-e2e (e2e.yaml's tiers modify host state -- see
# test/e2e/README.md -- so it stays opt-in), test-load (weekly, not per-PR),
# and the client-matrix macOS/Windows legs, which need those OSes.
#
# frontend-test is here because it was not, and the omission was invisible:
# the vitest suite ran in exactly one place, resilience.yaml's job named
# "Accessibility Tests", which runs `pnpm test` -- all twenty test files,
# only one of which is about accessibility. A contributor running
# `make pre-pr` executed no frontend test at all.
#
# test-migration is here for the same reason. It asserts the SQLite and
# Postgres schemas come out identical and that every down migration
# reverses its up; it was wired into no workflow whatsoever. The e2e tier-1
# matrix proves the app works on both backends, which is a different claim
# from the schemas agreeing.
ci-required: fmt-check check-gitignore lint lint-tagged lint-cross frontend-lint frontend-check frontend-test actionlint check-generated build pam test-pam lint-pam cover-ci cover-floors test-migration semgrep ## Every blocking check CI runs

# Advisory: govulncheck and pnpm audit report to the PR summary rather than
# blocking, because both can surface a dependency you cannot fix in the same
# PR. `-` keeps that behavior locally.
ci-advisory: ## The non-blocking scanners, run the way CI runs them
	-$(MAKE) govulncheck
	-$(MAKE) pnpm-audit

ci: ci-required ci-advisory ## ci-required plus the advisory scanners

##@ Misc

.PHONY: version update update-go-version
version: ## Print the version this tree would build as
	@echo "Version: $(VERSION)"

update: ## Update Go dependencies and tidy
	go get -u ./...
	go mod tidy

update-go-version:
	@echo "Must update go.mod and .github/docker/Dockerfile.devcontainer"
