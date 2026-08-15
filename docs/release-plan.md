# ssoossh release plan

Index for the phases that take the project from a working user-certificate
loop to a first tagged release covering **user certificates and `sudo`/`su`
through PAM**.

Supersedes the project's original delivery plan for everything still open.
That plan's phases 1 to 5 are done and are not repeated here. Its phase 6
(first release), phase 7 (PAM), and the PAM-relevant half of phase 10 (GA)
are consolidated below. Service certificates, host certificates, LDAP
enrichment, and the admin model move to a second release. The old plan's
files have since been removed from the tree; what mattered from them is
folded into the decisions and corrections below rather than linked.

## Scope: one release, two certificate types

User and PAM certificates belong in the same release because they are the
same product. Both are interactive, both are approved by a human in a
browser, both are short-lived, and both retain nothing afterwards. PAM
reuses the user path end to end: the same approval page, the same SSE
delivery, the same signer mapping onto `ssh.UserCert`. Adding it costs a
config section, a route, a switch case, and four client-side validation
checks.

Service and host certificates are the opposite kind of thing: unattended,
long-lived (the sample config suggests a year), and the reason enrollment
expiry, approver lifecycle, account-disabled sweeps, revocation, and an
admin role all exist. None of that is needed to ship SSH login and `sudo`,
and carrying it would delay both.

The shape of the release is therefore: **a person authenticates to a machine
with their identity provider, twice.** Once to open a shell, once to become
root.

## What this plan corrects

The old phase documents were written at different times and several of their
premises no longer hold. Verified against the tree at `d3ab775` on branch
`ai`:

| Old claim | Actual state |
| --- | --- |
| The default build is broken, everything needs `-tags=exclude_frontend` | `go build ./...` exits 0 with no tags. Phase 1's exit criterion holds. |
| `pam_ssoossh` does not compile: `auth.go:10`, `kp` declared and not used | Stale. `auth.go` was rewritten to fail closed with `errNotImplemented`; `kp` is used at `auth.go:44`. The old body is preserved as a commented reference with a written analysis of what it misses. |
| `auth.go` is ~80% commented out, live path is `var cert = ""` | Stale. That text is inside the reference comment, not the live function. |
| PAM is blocked on a dev VM, then on docker-outside-of-docker | Narrower than that. The host is `x86_64`, glibc 2.41, `libpam0g` installed. `CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared ./pam_ssoossh/` fails on exactly one thing: `security/pam_ext.h: No such file or directory`. Local iteration needs `libpam0g-dev` and `CGO_ENABLED=1`. The container is needed for **distribution** against old glibc, not for development. |
| The success-logging bug is unreachable today | Live. `pam_ssoossh.go:52-63` treats a nil error as success and logs `"successful authentication: %s"` before returning whatever code it was given. |
| Stale `.so` and `.h` are checked into the tree | `pam_ssoossh.so` is gitignored, `pam_ssoossh.h` is untracked. Working-tree debris, not committed artifacts. |
| `cookie_max_age` default of 0 is bad (ideas.md) | Already fixed. `defaultCookieMaxAge = 12 * time.Hour` at `server/bootstrap/router.go:270`, guarded by `if c.HTTP.CookieMaxAge > 0`; `_defaults.yaml` ships `300s`. |
| Did we resolve the CSRF finding? (ideas.md) | Yes. `server/middleware/csrf.go` and `csrf_test.go` exist; `security-review-2026-08-11.md` records finding 1 as fixed. |
| Approver client IP should be a key ID field (ideas.md) | Already implemented. `keyIDTemplateData.ClientIP` at `server/service/keyid.go:19`, fed from `req.SourceIP`, which is `g.ClientIP()` under a configured `SetTrustedProxies`. |
| Codegen chain needs hooking to the build (ideas.md) | Already gated. `make types-check` and `make openapi-check` both run in `build-test.yaml`. Generation stays a deliberate `make` step; CI fails the PR when the output drifts. |
| casbin is part of the stack (root CLAUDE.md) | Not in `go.mod` and imported nowhere in live code. The only hits are under `.claude/worktrees/`, which are stale copies. Strike the line. |

What is genuinely unstarted, and what this plan is mostly about: **there is no
`test/` directory**, so the end-to-end harness does not exist, and `docker`
is absent from the devcontainer, so the PAM release build has nowhere to run.

## Decisions

| Decision | Choice |
| --- | --- |
| Release contents | User certificates and PAM. Service, host, LDAP, admin move to release 2 |
| Certificate lifetime policy | **Fully deferred.** Flat per-type `valid_duration` stays. Both pieces that needed it (service retrieval subnet lock, host-owned source-network cap) belong to types not in this release |
| Admin and auditor roles | Release 2. Nothing here needs them: enrollment expiry is service-only, and user-disable is explicitly out of scope for hour-long certificates |
| PAM approval | Reuses the browser approval flow. The module prints the URL through the PAM conversation and blocks on SSE. No new server approval concept |
| PAM certificate type | Maps to `ssh.UserCert`. `model.CertificateTypePAM` already exists at `server/model/enums.go:17` |
| PAM local development | `libpam0g-dev` plus `CGO_ENABLED=1`. No container required to iterate |
| PAM release build | Old-glibc container via docker-outside-of-docker. `centos:7` or `oraclelinux:7` for amd64, `amazonlinux:2` for aarch64 (`.github/workflows/TODO.md`) |
| macOS artifacts | Out of scope until the Apple developer account is renewed |
| E2E merge gate | Go harness with an in-process OIDC provider, no Docker. The compose stack is a separate release rehearsal, not the PR gate |
| Signer topology | In-process on gochannel. `certmsg`, the zero-DB test, and `pipeline.go` stay as the split seam |
| Revocation | Accepted limitation, unchanged. Short lifetimes are the answer, and every type in this release is short-lived, so the argument holds without qualification here |

The revocation position is worth restating because this scope makes it
comfortable for the first time. The delivery plan had to argue that
revocation did not matter much while planning to ship year-long service and
host certificates. This release ships only certificates measured in seconds
to hours, so "they expire faster than a revocation list could be
distributed" is simply true rather than a trade being defended.

## Phases

| # | Phase | File | Depends on |
| --- | --- | --- | --- |
| 1 | Repo hygiene and release-blocking debts | [release-phase1-hygiene.md](release-phase1-hygiene.md) | none |
| 2 | End-to-end harness, the merge gate | [release-phase2-e2e.md](release-phase2-e2e.md) | 1 |
| 3 | PAM build environment | [release-phase3-pam-build-env.md](release-phase3-pam-build-env.md) | 1 |
| 4 | PAM certificate type, server side | [release-phase4-pam-server.md](release-phase4-pam-server.md) | 3 |
| 5 | PAM module and the four checks, client side | [release-phase5-pam-client.md](release-phase5-pam-client.md) | 3, 4 |
| 6 | Release artifacts and the tag | [release-phase6-artifacts.md](release-phase6-artifacts.md) | 5 |
| 7 | Deployment stack, documentation, rehearsal | [release-phase7-deploy-docs.md](release-phase7-deploy-docs.md) | 6 |

Phases 2 and 3 are independent and should run in parallel. Phase 3 is the
long-lead item (container plumbing, cross-compilation, CI runners) and phase
2 is the one that protects everything after it, so starting both at once is
worth more than finishing either sooner.

Phase 2 lands before any PAM work reaches the server for a specific reason:
phase 4 edits `Approve`, `certTypeFor`, and the config schema, all of which
the user path runs through. Without the harness in place, the first thing
PAM can break is the feature that already works.

## Ordering rationale

The old plan ordered by certificate type and put release engineering in the
middle. This one orders by **what protects what**:

1. **Hygiene first** because it is cheap and everything downstream assumes a
   clean tree and a committed set of documents.
2. **The harness second** because it is the only thing that will notice when
   PAM work regresses user certificates, and it is the phase 6 prerequisite
   the delivery plan already identified.
3. **Build environment before PAM code** because a module that cannot be
   compiled cannot be tested, and the four security checks in phase 5 are
   worth nothing without tests. The alternative, writing the checks blind and
   compiling them later, is how check 2 went missing the first time.
4. **Server before client** because the client's four checks are validated
   against certificates the server issues. Phase 4 ends with the PAM type
   reaching the signer, which is what phase 5 has to verify.
5. **Artifacts before documentation** because the deployment runbook
   documents installing the artifacts, and writing it against artifacts that
   do not exist yet produces a document nobody has followed.

## Cross-phase verification gates

Carried forward from the delivery plan, all still binding:

- `go build ./...` and `go test ./...` clean with **no build tags**, and
  `golangci-lint run` clean. From phase 3 this extends to
  `go build -tags=pam` and `golangci-lint run --build-tags pam`.
- `server/service/pipeline_test.go` stays green. It is the regression test
  for the whole signing pipeline and must not be weakened as the PAM type is
  added.
- `server/signer/zerodb_test.go` stays green. It is the only enforcement of
  the signer's zero-database boundary.
- **Any phase that changes the API regenerates what is generated from it**:
  `make openapi`, `make types`, and `go test ./server/webtypes/ -update`.
  `docs/openapi.yaml` and the frontend TypeScript are outputs, never
  hand-edited. CI's `openapi-check` and `types-check` fail the PR otherwise.
  Phase 4 adds a route and a config section; it owns this. See
  [wire-types.md](wire-types.md).
- From phase 2 onward, the end-to-end suite gates merges. A phase that turns
  it red is not done.
- `pam_ssoossh/` must not import `server/` or `client/`, only `internal/`
  (`.claude/rules/go.md`). Currently respected: its only project imports are
  `internal/version` and `internal/crypto/ssh/keypair`.

## Deferred to release 2

Choices, not oversights. Each already has a written plan.

| Item | Plan | Why it waits |
| --- | --- | --- |
| Service certificates and enrollment | none written; scope carried in this table | Drags in enrollment rows, multi-use redemption, approver lifecycle, and the subnet lock. None of it serves interactive login |
| Host certificates and principal mapping | none written; scope carried in this table | Needs the challenge transport, a `host_mappings` reshape on both dialects, and the only genuinely long-lived certificate the product issues |
| LDAP enrichment and account status | none written; scope carried in this table | Exists to expire enrollments when someone leaves. There are no enrollments in this release |
| Admin and auditor roles | [admin-authorization-plan.md](admin-authorization-plan.md) | Its one genuinely administrative function is enrollment expiry. Auditor is a read-only convenience nobody has asked for yet |
| Certificate lifetime and source-network policy | [certificate-lifetime-policy-plan.md](certificate-lifetime-policy-plan.md) | Both concrete consumers are service and host. `ForceCommand` and `SourceAddresses` keep being dropped unconditionally at approve |
| macOS artifacts | none written; scope carried in this table | Apple developer account. An un-notarized macOS binary is undistributable in practice |
| Multi-instance safety | [multi-instance-safety-plan.md](multi-instance-safety-plan.md) | Phase 2's persistent session secret was the piece worth pulling forward; the rest is not |
| Signer process split | [signer-split-deferred.md](signer-split-deferred.md) | The seam is maintained throughout. Splitting buys nothing at one instance |
| QR code for phone approval | ideas.md | Genuinely good, nothing depends on it |
| Client-side TLS pinning | none written; scope carried in this table | `buildTLSConfig` knows only `InsecureSkipVerify` |
| Console-login PAM module | [ssoossh-context.md](ssoossh-context.md) | The code-into-the-web-UI flow. `sudo`/`su` do not need it |
| `.down.sql` migrations | none written; scope carried in this table | None exist for either dialect |

Release 2 will need its own plan document once work on it starts — service
and host certificates in particular are substantial enough that "none
written" should not last long. This table is the scope record until then.

## Exit criteria for the release

- A tagged release publishes working client, server, and PAM artifacts for
  Linux, plus a Windows client.
- On a fresh machine brought up from the documentation alone: `ssh login`
  opens a shell against a real identity provider, and `sudo` on that machine
  triggers browser approval and succeeds.
- The end-to-end suite passes in CI on every pull request, covering both.
- Denying in the browser denies both the login and the `sudo`.
