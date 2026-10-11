---
title: Dependency scanning
description: The three scanners behind make security, which one gates a merge, and how to raise the bar.
sidebar:
  order: 3
---

`make security` runs three complementary scanners. One of them, semgrep, is a
merge gate; the other two are advisory, because both can surface a dependency
that cannot be fixed in the same pull request. This page is for whoever
decides how hard those gates should be.

## The scanners

### govulncheck (Go vulnerability checker)

- **Tool:** `golang.org/x/vuln/cmd/govulncheck`, baked into the build image.
- **Invocation:** `govulncheck ./...` (`make govulncheck`).
- **Config:** none. It checks the Go vulnerability database for known
  vulnerabilities in code the build actually reaches.
- **In CI:** advisory. The step runs with `continue-on-error: true` and posts
  its output to the job summary and as an artifact.

### pnpm audit (frontend dependencies)

- **Tool:** `pnpm audit`, in the `frontend/` directory.
- **Invocation:** `pnpm audit --audit-level high` (`make pnpm-audit`).
- **Severity:** `high` and `critical`; low and moderate are not reported.
- **In CI:** advisory, like govulncheck: `continue-on-error: true`, with the
  output in the job summary and as an artifact.

### semgrep (static analysis of the frontend)

- **Tool:** the `semgrep/semgrep:1.174.0` image, pinned in both the workflow
  and `scripts/semgrep.sh`.
- **Invocation:**
  `semgrep scan --error --metrics=off --config p/typescript --config p/javascript frontend/src`
  (`make semgrep`).
- **Config:** the two registry rule packs above. There is no `.semgrep.yaml`;
  `.semgrepignore` in the repo root excludes paths.
- **Scope:** the frontend only. The Go side is covered by `gosec` inside
  `golangci-lint`, so pointing semgrep at it would report the same findings
  twice.
- **In CI:** a **merge gate**. The job has no `continue-on-error`, and
  `--error` makes any finding fail it.

## Gates at a glance

| Scanner | Reports | In CI | Locally (`make`) |
| --- | --- | --- | --- |
| govulncheck | any reachable vulnerability | advisory | `ci-advisory` |
| pnpm audit | `high` and `critical` | advisory | `ci-advisory` |
| semgrep | any finding from the two rule packs | **blocks** | `ci-required` |

## Running locally

```bash
# All three
make security

# Individually
make govulncheck
make pnpm-audit
make semgrep
```

`make semgrep` needs Docker. It runs `scripts/semgrep.sh`, which copies the
sources into the container with `docker cp` rather than a bind mount: from a
devcontainer talking to the host's Docker daemon, a bind mount resolves on the
host and comes up empty for any worktree other than the one the container was
started from.

## In CI

`.github/workflows/security.yaml` runs on every pull request, on demand, and
on a weekly schedule (Mondays at 06:00 UTC) that re-checks dependencies nobody
touched against newly published advisories. It has no push trigger. Each
scanner is one job, and each runs only when its side of the tree changed:
govulncheck for Go changes, pnpm audit and semgrep for frontend changes.

The Makefile mirrors the split:

```make
ci-advisory:
	-$(MAKE) govulncheck
	-$(MAKE) pnpm-audit
```

The leading `-` reports a failure without stopping the run, matching CI's
`continue-on-error: true`. `semgrep` is in `ci-required` instead, alongside
`lint` and the other blocking checks.

## Raising the bar

1. **govulncheck or pnpm audit as a gate.** Remove `continue-on-error: true`
   from the step in `security.yaml`, and move the target from `ci-advisory`
   to `ci-required` in the `Makefile` so local runs agree.
2. **pnpm audit's threshold.** Change `--audit-level high` in both the
   workflow and the `pnpm-audit` target: `critical` to quieten it,
   `moderate` to widen it.
3. **semgrep's rules.** Add rule packs with further `--config` flags, in both
   `security.yaml` and `scripts/semgrep.sh`, which must stay in step.

## Related

- **Dependency updates** are automated: Dependabot opens weekly pull requests
  for Go modules, the frontend, the user docs and GitHub Actions
  (`.github/dependabot.yml`).
- **SBOMs** are not generated for releases yet (`sbom: false` in
  `.goreleaser.yml`).
