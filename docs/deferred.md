# Deferred, declined, and settled

Three kinds of thing, kept together because they answer the same question:
*why isn't this on a plan?*

1. **Deferred** — worth doing eventually, not now.
2. **Declined** — evaluated against this project's scale and rejected. Not
   oversights. Re-proposing these needs a reason the scale changed.
3. **Settled** — audited, found solid, and not to be reworked.

Consolidated 2026-08-22 from the security/API audit (2026-08-21), the
feature review (2026-08-22), and the comparative audit (2026-08-22).

## Deferred: tooling and process

From the comparative audit's benchmarking against hugo, goreleaser, and
pocket-id. Adopt opportunistically; none of these blocks anything.

| Change | What the exemplar does | Note |
| --- | --- | --- |
| Changelog automation via git-cliff | pocket-id: `cliff.toml`, automated from Conventional Commits. goreleaser: native changelog grouped by commit type. | **The lowest-friction candidate in the whole comparison.** ssoossh already writes Conventional Commits, which is git-cliff's only precondition; `cliff.toml` is a small mechanical config. Currently there is no changelog at all — `release-notes.md` is a hand-written draft. |
| Shell completion command | goreleaser registers `RegisterFlagCompletionFunc()` and ships a `completion` subcommand. | Standard Cobra feature, low effort. Verified absent from `client/cmd/` and `server/cmd/`. |
| Generated man pages | goreleaser generates them in the release pipeline via a hook script. | ssoossh's man pages (`docs/man/*.1`, `*.5`, `*.8`) are comprehensive but hand-maintained, with no generation target in the Makefile. This is a drift risk as flags change, not a present defect. |
| Committed dependency-scan config | goreleaser: `.grype.yaml` with an explicit severity gate that fails the build. | ssoossh already runs the equivalent checks (`make security` = `govulncheck` + `pnpm audit` + `semgrep`). The gap is that thresholds live as Makefile invocations rather than a reviewable config file. Minor formalization, not a missing capability. |
| `CONTRIBUTING.md` | All three reference projects have one. Two document an AI-assistance disclosure policy. | Only matters once ssoossh wants external contributors. `CLAUDE.md` and `.claude/rules/*.md` serve an adjacent but genuinely different audience — they instruct an AI assistant working in the repo, not a human deciding whether to submit a PR. |
| `AGENTS.md` (contributor-facing conventions) | hugo and pocket-id both have one. | Same audience caveat as above. |
| `THREAT_MODEL.md` / `INCIDENT_RESPONSE.md` | goreleaser only — **not** hugo, **not** pocket-id. | Not a majority practice among the three. ssoossh's `SECURITY.md` is already more detailed than hugo's or pocket-id's. The mitigations a threat model would describe already exist in the code; writing them up is formalization. Judgment call about audience: is anyone besides the maintainer going to read a threat model for a self-hosted homelab tool? |

## Deferred: features parked on purpose

- **Signer process split (NATS/mTLS).** Design exists
  ([signer-split-deferred.md](signer-split-deferred.md)) and the seam is
  preserved — `server/certmsg`, `server/signer`'s zero-database test,
  `server/bootstrap/pipeline.go`. No reason to start until there is an actual
  HA or key-isolation requirement. **Treat as correctly parked, not as debt.**
  Lowest priority of the three deferred design documents.
- **Mutation testing.** Explicitly sequenced, not skipped by omission.
  Mutation testing validates an existing suite's assertion strength; the
  right order is meaningful coverage first, then mutation testing. Introducing
  it earlier would have little to mutate against.
- **Code-specific rate limiting on service-enrollment redemption.**
  `/certs/service/retrieve` currently shares the global per-IP rate limit with
  no throttling specific to the enrollment code itself. Already recorded as an
  open question in [ssoossh-context.md](ssoossh-context.md). Revisit when
  service enrollment ships — a redemption endpoint that is reachable
  unattended wants a tighter bound than a human-driven one.
- **OpenAPI field-level polish.** Endpoint coverage in `openapi.yaml` is
  complete, but field descriptions are uneven and no example values are
  included. Polish, not a correctness gap. (Distinct from CI-validating the
  spec, which is in [changes-next.md](changes-next.md).)

## Declined: not applicable at this scale

The 2026-08-21 audit ran these methodologies and found nothing worth doing.
Recorded so they are not re-run from scratch.

| Area | Why not |
| --- | --- |
| API versioning | The correct choice for a single first-party client under active development is no versioning scheme. Absence is a decision, not a gap. |
| API gateway, service mesh, circuit breakers, distributed tracing | A single self-hosted binary serving its own routes. Nothing to front, nothing to mesh. |
| GraphQL | REST-only by design; no GraphQL anywhere in the stack. |
| WebSocket patterns | The real-time channel is SSE — one-directional, server-to-client certificate delivery. Different and simpler threat model, already covered under the API and auth audits. |
| `better-auth` | The project integrates OIDC directly via `coreos/go-oidc`. The library does not apply to this implementation. |
| Progressive web app, push notifications | The web UI is a login and approval flow, not an installable offline app. Real-time delivery already reaches the CLI over SSE. |
| Internationalization | Single-locale self-hosted admin tool. Revisit only if community deployments actually ask. |
| Design-system creation | One small UI, a handful of components, a single maintainer. Formal design system would be overhead with nobody to coordinate with. |
| API filtering/sorting, response optimization | No filtering need yet on a single user's own certificate list; gzip would save little on ~1KB responses. |
| Web performance audit | Two static pages under ~100 lines, no heavy dependencies, served same-origin as the API. Revisit only if real usage shows slowness, and start with DevTools rather than a formal audit. |
| `pkg/` directory | goreleaser has one because it is importable by third parties. ssoossh has no such goal — different product shape, not a missing convention. |
| UUIDs as Postgres `uuid` instead of `TEXT` | Costs ~20 bytes per key and gives up the type check. Not worth a migration on its own; worth knowing if the ID type is ever revisited. |
| Length limits on Postgres `TEXT` columns | On Postgres, `TEXT` and `VARCHAR(n)` are the same storage with the same index behavior, so this is not the anti-pattern it is on other engines. Input length belongs in validation at the API boundary — which is the `ValidatePrincipal` item in [changes-now.md](changes-now.md). |

## Settled: audited and solid, do not rework

The 2026-08-21 audit found **no critical correctness or auth-bypass
findings**. These specific areas were examined closely and are correct.
Changing them needs a reason beyond style.

- **Certificate request binding.** Atomic claim-on-approve
  (`UPDATE ... WHERE user_id IS NULL`) prevents two admins contesting
  ownership. Principals derive from the **approver's** identity, not the
  requester's — this is precisely what stops a user approving themselves into
  elevated principals.
- **Options narrowing.** Requested options are intersected against server
  config, never trusted as-is. `ForceCommand` and `SourceAddresses` are
  dropped entirely (fail-closed) until a lifetime policy exists. Matches the
  documented "server config is the outer bound" hard constraint.
- **CSRF.** Deliberately uses `Sec-Fetch-Site` plus an `Origin` fallback
  rather than synchronizer tokens (`server/middleware/csrf.go`), reasoned
  explicitly for homelab multi-origin deployments. No client-side token to
  steal; the frontend correctly does nothing.
- **Sessions.** `HttpOnly`, `Secure` (TLS-aware), `SameSite=Strict`,
  GORM-backed store, clean server-side revocation on logout, session secret
  generated once and race-guarded across instances.
- **OIDC flow.** State and nonce are 32 random bytes, single-use, validated
  before callback processing. ID token signature, audience, and expiry
  verified. `return_to` rejects absolute and protocol-relative URLs, closing
  the obvious open redirect.
- **CSP and related headers.** Per-request nonce, `default-src 'self'`,
  `frame-ancestors 'none'`, HSTS, `Cache-Control: private, no-store`, CORS
  narrowly scoped to OIDC well-known endpoints. (The three *missing* headers
  are a separate item in [changes-now.md](changes-now.md) — this entry is
  about what is already right.)
- **Frontend XSS surface.** No `{@html}`, no `innerHTML`, no `eval`. Relies on
  Svelte's auto-escaping. Return-URL validation duplicated client- and
  server-side.
- **Secrets handling.** The CA key never enters process memory — signing is
  delegated to ssh-agent. No hardcoded secrets found.
- **File permissions.** Client-written private keys `0600`, public keys and
  certificates `0644`, correct in every path checked.
- **Database fundamentals.** Explicit `golang-migrate` migrations (not
  AutoMigrate) with a startup version-skew guard; driver-aware
  `clause.OnConflict` upserts; JSON deliberately kept as portable TEXT rather
  than dialect-specific JSONB; no raw-SQL string concatenation anywhere; every
  table has a primary key; schema is at 3NF with both denormalizations
  documented and justified. **Two caveats** the 2026-08-22 schema audit
  added: there are no down migrations (a violation of the project's own
  `.claude/rules/database.md`), and index coverage does not match the actual
  query shapes. Both are in [changes-next.md](changes-next.md); the
  fundamentals above still stand.
- **`certificate_request_decisions` duplicating identity fields from
  `users`.** Looks like a 3NF violation and is not — it is a deliberate
  point-in-time snapshot so a historical decision cannot be rewritten by a
  later change to the users table, reasoned out in the model's doc comment.
  Correct as designed.
- **`sessions` outside the migration scheme.** `gormsessions.NewStore`
  `AutoMigrate`s its own table on startup. A deliberate, documented, narrow
  exception (`server/bootstrap/router.go:167-175`) — the table is wholly
  owned by the library, not by `model/`. No action.
- **Mass assignment.** Every handler binds to an `apitypes.*` DTO, never a
  model struct directly. No mass-assignment surface.
- **Package boundaries.** No cross-imports between `server/`, `client/`, and
  `pam_ssoossh/` outside `internal/`, matching `.claude/rules/go.md`.
- **Monorepo layout.** Already closely mirrors pocket-id, the closest
  functional analog, and is a defensible middle ground between goreleaser's
  stricter `cmd`/`internal`/`pkg` split and hugo's flat convention-only
  approach. No structural gap.
- **Health checks.** `/healthz` and `/ping` exist, plus systemd readiness
  notification.
- **Frontend tests exist.** The 2026-08-21 audit reported "zero tests
  anywhere in `frontend/src/`" and recommended writing a first pass. That
  finding is **stale and should not be actioned** — the 2026-08-22 feature
  review found tests present, and this was re-verified on 2026-08-22:
  `approval.test.ts`, `format.test.ts`, `paths.test.ts`, `client.test.ts`,
  `endpoints.test.ts`, `ApprovalView.test.ts`, `ConsentModal.test.ts`. Worth
  confirming the coverage is more than a token pass, but the gap is closed.
- **Test and documentation discipline.** Every `exclude-from-coverage.txt`
  entry has a corresponding in-code comment explaining why. The PAM module's
  cgo entry points are correctly treated as untestable in Go with a
  documented manual C harness instead. Wire types are kept in sync across
  server, Go client, and web UI by generation plus a golden test that fails
  on drift. The audit called this "unusually disciplined for a pre-1.0,
  single-maintainer project — worth maintaining as a practice, not something
  to template over."

## Settled: already built

Verified in code by the 2026-08-22 feature review, not merely claimed in
docs. Listed because several of these are *ahead* of what the older design
documents describe, and a planner reading only the design docs would
otherwise schedule work that is already done.

- **User certificates**, full pipeline: `ssh login`/`logout`/`proxycommand`,
  OIDC auth, browser approval, SSE delivery, signing. Driven end to end by
  `server/service/pipeline_test.go`.
- **PAM (`sudo`/`su`) certificates** — fully wired, with FIPS-approval and
  glibc-floor build work landed. **Note:** `signing-pipeline.md` is stale here;
  it still groups "Host / PAM — rejected outright at Approve." PAM is not
  rejected. Host still is.
- **FIPS-approved key policy** — shared `internal/fipsmode`, enforced across
  client key generation, server signing, TLS cipher suites and curves, and the
  bootstrap CA key check.
- **Client settings enforcement** — `enforce` YAML, Windows Group Policy
  registry keys, macOS managed-preferences plist, full precedence chain.
- **Cert-type policy centralization** — `server/service/certtypepolicy.go`
  replaced the scattered per-type switches.
- **Certificate key ID templates** — per-type `text/template` config, parsed at
  startup, with a fallback chain.
- **Wire-type sync** across server, Go client, and web UI — shared
  `internal/apitypes`, tygo generation, golden test, OpenAPI generation, all
  CI-checked.
- **E2E test harness** — all three tiers, own harness for IdP/server/agent/
  sshd/browser, gating merges via `.github/workflows/e2e.yaml`.
- **CI pipeline** — separate `build`, `codecover`, `e2e`, `lint`, `security`
  workflows.
- **Deployment artifacts** — `deploy/docker-compose.yml`,
  `deploy/ssoosshd.service`, and Linux/Windows/macOS client packaging with
  macOS signed and notarized.

## Audit coverage gaps

What the 2026-08-21 audit did **not** independently verify. Not findings —
known limits on how far that audit's assurance extends.

- Every error-throwing path in `server/service/` beyond the auth and
  certificate-request flows.
- The request-binding race condition under real concurrent load. Code review
  only, no load test.
- `signer/signing.go`'s cryptographic signing step itself. The audit covered
  the *authorization decision* to sign, not the signing implementation.
- Actual resolved frontend dependency versions — see the `pnpm audit` item in
  [changes-now.md](changes-now.md).
