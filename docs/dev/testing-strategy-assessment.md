# Testing Strategy Assessment

Assessment of 18 proposed testing investments for ssoossh. Each item includes feasibility, expected yield, build cost, recurring cost, and verdict.

## 1. Go Mutation Testing (gremlins)

**Status:** In progress

Feasibility: High (gremlins runs under Go 1.26 per task baseline; mutation-testing-findings.md used manual mutation approach successfully)

Expected Yield: Very low to moderate. Manual mutation testing on server/service/ already caught one weak test (TTL enforcement) and one untested guard (binding race). Go mutation testing would systematize this for the entire codebase. However: security-critical packages (crypto/ssh, middleware, service) are already 90%+ covered. High-coverage packages like server/middleware (94.9%) may still have weak assertions, but mutation testing will only confirm what code review + focused test reading already identifies. The true problem is not missing mutations but missing test isolation (see Item 8).

Build Cost: High. The task explicitly notes empirical cost: 3.9GB scratch disk per run (already caused one fleet outage), and per-package runtimes unknown. This is not amortizable.

Recurring Cost: Critical blocker. CI runners would need enlarged disk quotas. Each developer run tests runner patience. At current scale, disk cost exceeds the signal.

Verdict: **Drop for now, revisit at next scale.** The one finding mutation testing would have caught (weak TTL test) was found and fixed manually via mutation-testing-findings.md. Manual mutation on security boundaries (Item 12's policy engines) is proportionate; automated gremlins across the full tree at 3.9GB/run is not. Defer until either: (a) a maintained Go mutation tool with lower disk overhead emerges, or (b) a measured performance problem in server/service or server/middleware surfaces that mutation testing is required to verify fixes for.

## 2. Frontend Mutation Testing (Stryker)

**Status:** Configured, blocked

Feasibility: Low. Stryker 8.7.1 + vitest + Svelte 5.56.9 are incompatible. Stryker's instrumentation expects Svelte's `walk()` utility, which Svelte 5 no longer exports. Stryker hangs on execution (180+ second timeout, no output). Configuration is in place but non-functional.

Expected Yield: Moderate if it worked. 16 frontend test files exist (approval.test.ts, format.test.ts, paths.test.ts, client.test.ts, endpoints.test.ts, ApprovalView.test.ts, ConsentModal.test.ts, CertDetailModal.test.ts, CertRow.test.ts, PageHeading.test.ts, ThemeToggle.test.ts, UserMenu.test.ts, page.test.ts in multiple routes). Mutation testing would validate assertion strength on approval flow, cert rendering, and auth guards — high-value targets.

Build Cost: Moderate if tool becomes compatible. Stryker config already exists; waiting for Stryker 10.x or later to add Svelte 5 support.

Recurring Cost: CI time and flake risk unknown until tool works.

Verdict: **Defer until tool support arrives.** mutation-testing-findings.md explicitly recommends "revisit when Stryker 10.x or later adds Svelte 5 support." Do not invest in workarounds (downgrade Svelte, fork Stryker, etc.) until the dependency mismatch is confirmed unfixable by waiting.

## 3. Fuzzing (Parsers)

**Status:** Proposed

Feasibility: High. Go native fuzzing is built-in (go test -fuzz). Targets identified: SVG/logo branding (isSVG, keyid template parsing), principals map (internal/principalsmap), cert options/extensions (server/service/certoptions.go), lifetime policy (server/service/lifetimepolicy.go), config YAML (spf13/viper), macOS plist (server/client/config), OIDC claims, wire types, SSH key parsing. SVG path especially high-value: commits 37e0966 (fix: require SVG root element) and 818f6ba (refactor: split XML prologue skip) document recent bugs in substring matching and prologue skipping that fuzzing would catch.

Expected Yield: Medium-high. The SVG branding path produced two real bugs in the last 500 commits—that's historical evidence of value. Keyid templates, principals, lifetime policy, and wire types all process untrusted input without explicit fuzz tests. Certificate options parsing (intersection logic, extension narrowing) touches the security boundary. SSH key parsing is a standard target.

Build Cost: Low. Fuzz targets are minimal; use Go's testing.B-style corpus addition pattern.

Recurring Cost: Low. Fuzzing is fast (subsecond per target) and reproducible. One caveat: if a crash corpus grows large, integration into CI may require a fuzz-sweep target in Makefile + a weekly cron job rather than per-PR run.

Verdict: **Build now.** Start with SVG branding and keyid templates (proven bug history), add principals + lifetime policy (security boundary), wire types (cross-cutting validation). Quick wins with clear payoff. Pair with mutation testing on the same packages (Item 12) to verify fixes.

## 4. Postgres First-Class E2E Backend

**Status:** In progress

Feasibility: High. Test infrastructure (db_test.go with per-test Postgres schemas) already exists. Down migrations are deferred; up migrations are complete. Dialect parity enforcement is open (Item 16), but the schema itself is identical (verified via diff).

Expected Yield: Medium. Current e2e explicitly excludes Postgres ("worth a nightly run once multi-instance-safety-plan.md is acted on"). SQLite + Postgres test both dialects at the service layer (22 db_test.go migrations + bootstrap tests). The live correctness bug (Item 16: time.Time UTC normalization) was caught by reading both dialects' behavior, not e2e testing. New yield: concurrency patterns under Postgres (connection pooling, FK enforcement under concurrent writes), true multi-instance failover if Item 5 lands.

Build Cost: Moderate. Postgres container required. Database-schema-audit-2026-08-22 finding 1 already fixed; finding 2 (certificate_request_id link) already fixed; finding 7 (indexes) already fixed. No active blockers identified.

Recurring Cost: CI runner must have Postgres container available (already used in codecover.yaml); add ~2 min per full test run.

Verdict: **Build now; low priority relative to Items 5, 8.** This is a prerequisite for multi-instance concurrency (Item 5), but do not merge until Item 5 lands. Start Postgres e2e in parallel, run it nightly in CI after Item 5 ships, keep it separate from per-PR gate (Item 5 should gate merges).

## 5. Multi-Instance / HA Concurrency

**Status:** Proposed; design complete (multi-instance-safety-plan.md)

Feasibility: High. Design is precise and sequenced: (1) `Wait` decodes wake payload (testable today against gochannel), (2) add `signing_started_at` column, (3) NATS transport with queue groups, (4) startup validation. Test harness exists: two `CertRequestService` instances over shared database + gochannel transport. No NATS required for initial verification.

Expected Yield: High. The central bug is identified precisely: multi-instance clients lose certificates when landing on a different instance than the approver (410 + retry). Wake message already carries certificate payload; `Wait` just needs to decode it. This fixes a load-balancer-breaking intermittent failure. Secondary gains: scheduler double-execution prevention, serial allocation safety under concurrent issuance (both idempotent-ish but not explicitly tested for concurrent hazards).

Build Cost: Moderate. Requires careful test design (two service instances, shared state, assertion of cross-instance delivery). The multi-instance-safety-plan.md recommends testing against gochannel first (no NATS), which is low infrastructure cost.

Recurring Cost: CI minutes for concurrency tests + risk of flake if test timing is brittle. Design mitigates flake: use synchronization primitives (channels, mutexes) over sleep-based waits.

Verdict: **Build now.** This is a correctness fix for load-balanced deployments, not a nice-to-have. The design is complete and the test harness is simple. Sequence: implement step 1 (payload decode), gate merges on new concurrency test, then implement steps 2-4. Pair with Item 4 (Postgres e2e) to verify under both dialects.

## 6. PAM End-to-End in Container

**Status:** In progress (planning stage)

Feasibility: Medium. PAM module is already 78% covered at the unit level (cgo entry points correctly documented as untestable in Go with pam_ssoossh/testing/pamtest.c harness). Container e2e would repeat what unit tests already cover at lower speed. The gap: integration with the actual `sudo`/`su` flow. Buildable but requires amd64 container + root context.

Expected Yield: Low to moderate. Unit tests already exercise principal derivation, TTL enforcement, and extension narrowing on the PAM path. E2E would add: (1) PAM stack integration (is our plugin wired correctly into the stack?), (2) actual `sudo`/`su` acceptance/rejection, (3) credential caching. These are real but narrower than they sound: (1) is a one-time config check, (2) is a yes/no call, (3) is tested by the cache_test.go suite.

Build Cost: Moderate-high. Requires dedicated container image (already referenced as `pam-builder` in build.yaml but may not be maintained). Cross-architecture complexity (amd64 + arm64).

Recurring Cost: CI runners must support container + root. Current build.yaml shows PAM already builds on two architectures; e2e adds container overhead + flake risk if `sudo`/`su` behavior varies across runner images.

Verdict: **Defer unless a user reports PAM issuance failure.** The 78% unit coverage already validates the core logic. E2E repeats what's covered. Revisit when the first production deployment reports a PAM integration issue (e.g., sudo not accepting certs, principal mapping bugs).

## 7. Cross-Platform Client Matrix

**Status:** In progress (planning stage)

Feasibility: Medium-high for GitHub runners (macos-latest + windows-latest available). The client code is already written to be cross-platform (Pageant, WSL relay, plist policy, Windows policy). Client binaries are built on all three platforms in build.yaml. The gap is integration testing beyond compile-time validation.

Expected Yield: Medium. Real bugs caught: Pageant IPC correctness, WSL relay socket handling, plist parsing (macOS policy), registry querying (Windows policy). These are real but localized: Pageant is a thin wrapper around ssh-agent protocol, WSL relay is a socket reader/writer, plist/registry are parsed by standard libraries. The risk is not high relative to the complexity.

Build Cost: High. macOS runners cost 10x per minute; Windows runners cost 2-3x. GitHub Actions pricing makes extensive cross-platform testing expensive. Estimated 15-20 min per workflow run.

Recurring Cost: High CI minutes. Flake risk on macos/windows is traditionally higher than Linux (timing, environment variance). Maintenance burden to debug CI failures on platforms the project maintainer doesn't use daily.

Verdict: **Defer until critical mass of users.** The e2e-testing-plan.md explicitly excludes it: "not before the Linux path is trustworthy." Linux e2e (Items 1-3 in e2e plan) must be rock-solid first. When the first Windows or macOS user reports an issue, revisit. Until then, rely on compile-time validation + community testing.

## 8. Chaos / Failure Injection

**Status:** In progress (goroutine leak fix being implemented)

Feasibility: High. The three goroutine leaks are confirmed and being fixed (agent-a535e3a89805c910a working on context-cancellation in rate_limit.go:31, endpoint_rate_limit.go:28, endpoint_rate_limit.go:72). Graceful shutdown testing is straightforward: spawn a server, cancel context, assert all goroutines terminate. Clock/time injection is standard (time.Now() mocking for sweep tests). DB failure injection requires interface mocking (already in place for pgxpool).

Expected Yield: High. The rate limiter goroutine leaks block graceful shutdown—a correctness bug that would surface under load or container orchestration with tight shutdown deadlines. Chaos testing would catch: (1) goroutine leaks (confirmed), (2) unclosed channels in pub/sub (gochannel cleanup), (3) DB connection pool leaks on error. These are real operational failures that the test suite currently masks via mocks.

Build Cost: Low. Goroutine leaks are already being fixed. Chaos test harness is ~100 lines (spawn, cancel, pprof.Lookup for goroutine count, assert delta == 0). DB error injection uses interfaces already in place.

Recurring Cost: Low. Graceful shutdown test is deterministic and fast (~1 sec). Clock injection tests are isolated to specific scenarios.

Verdict: **Build now.** The goroutine leak fix is already in flight. Write a graceful shutdown test immediately after that merges. Add DB error injection and pub/sub cleanup checks as follow-ups. This catches real operational defects.

## 9. Load / Soak Testing

**Status:** In progress (agent-aa64f229b6cd879c4 implementing resilience + load suites)

Feasibility: High. Harness already exists (test/e2e/ with idp.go, server.go, client.go). Load tests are straightforward: N concurrent logins, rate limiter throughput, SSE fan-out limit, fd/goroutine leaks under sustained traffic. Soak tests: 1000s logins over 1h, certificate reuse under load.

Expected Yield: High. Validates: (1) rate limiter correctness under concurrency (separate from goroutine leak—this is about token-bucket math), (2) SSE subscription limits (does fan-out break at 100+ waiting clients?), (3) DB connection pool tuning (pool defaults are untuned), (4) fd leak detection (pprof against file descriptors). These are load-specific and not testable at unit scale.

Build Cost: Moderate. Load test harness is ~500 lines (concurrent logins + metrics collection). Soak is same harness, different parameters.

Recurring Cost: Cannot run every PR (time + resource cost). Should be nightly in CI or on-demand. Flake risk if assertions are tight (avoid magic numbers, use percentiles).

Verdict: **Build now; run nightly, not per-PR.** The harness exists and the agent is implementing this. Gate on completion, not on perfection. Start with concurrent-login + rate-limiter throughput (highest-value), add SSE + fd leak checks after. De-gate from PR merge once stable (2-3 weeks of green runs).

## 10. Automated Accessibility

**Status:** Already addressed

Feasibility: N/A - already planned

Expected Yield: N/A

Build Cost: N/A

Recurring Cost: N/A

Verdict: **Already queued.** Add a global `:focus-visible` outline rule, and `aria-busy`/`aria-live` on approve/deny buttons. This is 3 lines of CSS/HTML. The web UI is approval-focused and needs this for WCAG 2.4.7 compliance. Implement immediately after Item 8 (goroutine fix) merges, do not wait for a separate accessibility testing framework.

## 11. Browser Matrix Beyond Chrome

**Status:** Proposed

Feasibility: Medium (Firefox, WebKit available via Playwright/Puppeteer)

Expected Yield: Low. The SPA is simple: ~100 LOC, auto-escaping Svelte, no `{@html}` or `innerHTML`. CSP, SameSite, and ITP differences are real but secondary to the core flow. Approval page works in any modern browser.

Build Cost: Moderate. Playwright + WebKit + Firefox + Chrome adds 30MB to CI. Test runtime multiplies by 3-4.

Recurring Cost: High CI minutes (multiply e2e by 3). Flake risk on Webkit (historically less stable). Maintenance burden for WebKit-specific bugs.

Verdict: **Defer.** Priorities are Linux e2e (stable) >> cross-platform (high cost, lower ROI). Revisit after Item 7 lands and if users report browser-specific CSP/ITP failures.

## 12. Property-Based Testing

**Status:** Proposed

Feasibility: High (Go's testing/quick built-in, no external dependencies)

Expected Yield: Low to moderate, but specialized to policy engines. Lifetime policy (server/service/lifetimepolicy.go 11.6K) and cert-type policy (certtypepolicy.go 3.9K) have table-driven tests (17K and 5.4K in _test.go files). Property testing would add: "for all valid policy configs, derive correct lifetime" and "for all cert types, apply correct policy." But: table-driven tests already cover the matrix. Property testing duplicates that work unless it generates policies an author never hand-wrote.

Build Cost: Low. 100-200 lines per policy engine.

Recurring Cost: Low. Property tests are fast and deterministic.

Verdict: **Defer to Item 3 (Fuzzing).** Fuzzing the config YAML parser and the policy engines together is higher-value than property testing them in isolation. Table-driven tests are already thorough. If a policy bug slips through post-ship, revisit property testing as part of RCA.

## 13. Golden-File / Snapshot Tests

**Status:** Proposed

Feasibility: High (Go standard tooling with go-testdeep or write-to-file pattern)

Expected Yield: Low. CLI output (ssoossh principals, ssoossh version, man pages) is stable. Snapshot tests would catch unintended string changes. But: gendocs is already tested (commit b666959 added comprehensive coverage for man page generation). CLI flags are covered by cobra/pflags tests. Overkill for the scale.

Build Cost: Low. Snapshot pattern is ~50 lines per output type.

Recurring Cost: Very low. Snapshots are checked in; no external storage. Flake risk is zero (deterministic comparison).

Verdict: **Defer.** The three outputs that matter (CLI help, man pages, generated docs) are already tested via gendocs_test.go. Snapshot testing adds ceremony without catching new bugs. Revisit only if CLI output churn becomes a review burden (currently it is not).

## 14. Contract Tests

**Status:** Proposed; partially covered

Feasibility: High (openapi-check and types-check already in CI; schema parity is open)

Expected Yield: Low. The residual gap: openapi.yaml vs handler implementations are validated by openapi-check. TypeScript types vs Go types validated by types-check (tygo generation + golden test). Residual risk: OpenAPI field descriptions (uneven), example values (not included), and schema parity between Postgres/SQLite (open). The last is non-trivial; the first two are cosmetic.

Build Cost: Low for the cosmetic items (add examples, prose). Medium for schema parity test (parse both .sql files, compare column sets).

Recurring Cost: Low. Parity test is deterministic.

Verdict: **Defer the cosmetic polish. Pair the parity test with Postgres e2e.** The contract test surface is 80% covered today. The remaining 20% is low-risk and low-cost; do it when Postgres e2e lands.

## 15. Release Rehearsal

**Status:** Proposed

Feasibility: High (goreleaser already in build.yaml; compose stack exists in deploy/)

Expected Yield: Moderate. Current CI validates: compilation on all platforms (build.yaml), binary signing (PAM build), docker image (deferred to release tag). Missing: integration against real pocket-id (only test harness IdP exists), end-to-end flow on a fresh install (docker-compose deploy + login + cert issuance), client package install + update path on macOS/Windows. These are pre-release checks, not merge gates.

Build Cost: Moderate. Compose stack + pocket-id container + fresh database + cleanup. Estimated 5-10 minutes.

Recurring Cost: Manual (on-demand before release) or scheduled weekly. Not per-PR.

Verdict: **Defer until first release.** This is a release-readiness check, not a development test. Document the rehearsal checklist (build/sign, deploy stack, test flow, validate packages) in a docs/release-rehearsal.md now; execute manually before the v1.0 tag. Automate once the release cadence stabilizes (e.g., weekly snapshots).

## 16. Coverage Gating in CI

**Status:** Proposed

Feasibility: High (codecover.yaml already runs coverage, and since the exclusion list was removed the number it reports is the real one)

Expected Yield: Low. The real question: is coverage a useful gate, or does it train people to write low-quality tests that hit lines? Evidence: the exclusion list this repo used to carry looked disciplined (every entry had a comment) and was still 51/89 dead, silently excluding nothing; rate_limit_test.go explicitly works around the goroutine leak (line 32-34 comment) rather than fixing it. High coverage numbers can mask weak tests. The 90%+ packages are security-critical (crypto/ssh, middleware, service), so the gate would protect the right places. But coverage gating is not currently treated as a blocker.

Build Cost: Trivial. Add a threshold to codecover.yaml.

Recurring Cost: Maintenance. Uncoverable code (error paths, CGO) stays in the denominator and has to be argued about per package. False negatives (weak tests) are hard to detect automatically.

Verdict: **Build now, but conservatively.** Gate on <85% (current state: many packages are 90%+), not 90%+, to avoid false positives. Require PR review of any new `not covered:` comment. Re-examine the gate quarterly (is it catching real issues, or training worse behavior?). Pair with Item 3 (fuzzing) to validate that high coverage means something.

## 17. Benchmark Regression Tracking

**Status:** Proposed

Feasibility: High (Go benchmarking built-in; regression tools available)

Expected Yield: Low. The signing path (server/signer/sign.go) is the only performance-sensitive code called on every certificate issuance. Current baseline: no benchmarks, no regression tracking. A benchmark would catch: asymmetric key operations that unexpectedly slow (e.g., FIPS-mode overhead, key size change), SSH key parsing overhead (if it ever handles large request volumes), rate limiter token-bucket performance. Real but niche: signing is per-issuance (not per-request), and issuance is a human-driven approval flow, not a hot path.

Build Cost: Low. Write 5-10 benchmarks in signer_test.go.

Recurring Cost: Very low. Benchmarks run locally; CI benchmark regression tracking requires separate tooling (benchstat, gobenchdata) and infrastructure.

Verdict: **Defer.** No performance requirement exists. If the signing path becomes a bottleneck under load (Item 9 findings), revisit. For now, profile on demand (pprof is built-in) rather than automate tracking.

## 18. Static Analysis Expansion

**Status:** Proposed

Feasibility: High (golangci-lint is already configured; custom semgrep rules are straightforward)

Expected Yield: Low to moderate. Current setup includes golangci-lint with linters (see .golangci.yml) and semgrep (security.yaml). Gaps: custom semgrep rules for project-specific invariants (no global state except slog, no interface{}, no init(), struct-based DI) are not enforced at lint time. These are currently enforced by code review (.claude/rules/go.md). Static enforcement would catch: forgotten context.Context in I/O functions, hardcoded error strings (should use errors.New), missing error wrapping (should use %w). Real but catchable: CLAUDE.md is explicit about these rules, so violations are rare.

Build Cost: Moderate. Write 5-10 semgrep rules mirroring .claude/rules/go.md and test them against the codebase.

Recurring Cost: Low. Semgrep runs in ~30 sec on CI.

Verdict: **Defer.** Code review is currently catching these invariants. Automate only if violations become frequent (currently they do not). If Item 6 (external contributors) eventually lands, revisit to replace code review with automated checks.

## Summary Table

Ranked by value-per-unit-cost (highest priority first):

| # | Technique | Build Cost | Recurring Cost | Expected Yield | Verdict | Status |
|---|-----------|-----------|----------------|-----------------|---------|--------|
| 8 | Graceful Shutdown + Goroutine Leak Fix | Low | Low | High | **Build now** | In flight |
| 5 | Multi-Instance Concurrency | Moderate | Low | High | **Build now** | Designed |
| 3 | Fuzzing (SVG, keyid, principals, policy) | Low | Low | Medium-high | **Build now** | Proposed |
| 9 | Load/Soak Testing | Moderate | Low | High | **Build now** | In flight |
| 10 | Accessibility (focus-visible, aria-*) | Trivial | None | Medium | **Already planned** | Queue now |
| 16 | Coverage Gating (conservative) | Trivial | Low | Low | **Build now** | Proposed |
| 4 | Postgres First-Class E2E | Moderate | Low | Medium | **Build after 5** | Infrastructure ready |
| 14 | Schema Parity Test | Moderate | Low | Low | **Pair with 4** | Proposed |
| 6 | PAM E2E Container | Moderate | High | Low | **Defer** | In flight |
| 7 | Cross-Platform Client CI | High | Very High | Medium | **Defer** | In flight |
| 2 | Frontend Mutation (Stryker) | Low | Medium | Moderate | **Defer to v10** | Blocked |
| 1 | Go Mutation Testing | High | High | Low | **Drop** | Proposed |
| 11 | Browser Matrix (FF/WebKit) | Moderate | Very High | Low | **Defer** | Proposed |
| 12 | Property-Based Testing | Low | Low | Low | **Defer to fuzz** | Proposed |
| 13 | Golden-File Tests | Low | Very Low | Low | **Defer** | Proposed |
| 15 | Release Rehearsal | Moderate | Manual | Moderate | **Defer to v1.0** | Proposed |
| 17 | Benchmark Regression | Low | Low | Low | **Defer** | Proposed |
| 18 | Custom Semgrep Rules | Moderate | Low | Low | **Defer** | Proposed |

## The Drop List

Two branches should be discarded:

1. **Agent 1 (Go Mutation Testing / gremlins):** 3.9GB scratch disk per run, no actionable signal beyond what manual mutation + code review already found. The TTL test bug was caught and fixed via mutation-testing-findings.md. Disk cost prohibitive at current scale.

2. **Agent 6 / Item 6 (PAM E2E Container):** 78% unit coverage already validates PAM logic. E2E repeats what's covered at 3x execution time. Revisit only when a user reports a PAM integration failure in production.

The other four branches (Items 3, 4, 5, 8, 9 + Item 10 accessibility) are keepers: they address real gaps (fuzzing, multi-instance, graceful shutdown, load testing, accessibility) with clear payoff.

## Top Three Investments by Value-per-Cost

1. **Item 8: Graceful Shutdown Testing** (low build, low recurring, high yield). The three goroutine leaks in rate limiters are being fixed (agent in flight). Add a post-fix test that spawns the server, cancels context, asserts goroutine count returns to baseline. Catches operational defects that the mock-based suite masks.

2. **Item 5: Multi-Instance Concurrency** (moderate build, low recurring, high yield). Design is complete. The central bug is precise: clients lose certificates when landing on a different instance than the approver. Wake message already carries the payload; `Wait` just needs to decode it. Fixes a load-balancer-breaking intermittent failure.

3. **Item 3: Fuzzing (SVG, keyid, principals)** (low build, low recurring, medium-high yield). SVG path produced two bugs in 500 commits (proven history). Keyid templates, principals, and lifetime policy all parse untrusted input. Quick wins with fuzz targets (<100 lines each). Start here, pair with Item 12 (property-based) later to validate fixes.

## Real Bugs Noticed in Reading

1. **Goroutine leaks in rate limiters** (rate_limit.go:31, endpoint_rate_limit.go:28, 72): Three `cleanupClients` goroutines spawned once per middleware constructor, run forever, never stop. Block graceful shutdown. Fix in flight (agent-a535e3a); test coverage masked by workaround (rate_limit_test.go:32-34 explicitly works around the leak rather than fixing it).

2. **Middleware 94.9% coverage masks weak assertions:** rate_limit_test.go knows about and works around the goroutine leak. Coverage number is misleading when the test accommodates the bug rather than catching it. Mutation testing would have caught this earlier; graceful shutdown testing will catch similar issues in future.

3. **SVG branding parser not production-ready until recently** (commits 37e0966, 818f6ba): Two bugs in substring matching and XML prologue handling in the last 2 weeks. This path will benefit from fuzzing immediately.
