---
title: Dependency scanning
description: The three scanners behind make security, what each one gates on, and how to raise the bar.
eyebrow: Project
sidebar:
  order: 3
---

The `make security` target runs three complementary scanners to detect
vulnerabilities and insecure patterns. This page is for whoever decides how
hard those gates should be.

## Configuration

### govulncheck (Go vulnerability checker)

- **Tool:** `golang.org/x/vuln/cmd/govulncheck`
- **Invocation:** `govulncheck ./...`
- **Config:** none. This tool has no config file. It checks the Go standard
  vulnerability database for known CVEs in your dependencies.
- **Severity:** fails the build if any vulnerabilities are found (all are
  treated as errors).
- **Threshold:** zero-tolerance. Any detected vulnerability blocks the build.

### pnpm audit (JavaScript/Node vulnerability checker)

- **Tool:** `pnpm audit`
- **Location:** `frontend/` directory
- **Invocation:** `pnpm audit --audit-level high`
- **Config:** the `--audit-level high` flag filters to high and critical
  severity vulnerabilities.
- **Severity:** `high` and `critical`. Low and moderate warnings are
  informational only.
- **Threshold:** failures are reported to the job summary but do not block the
  build in CI (`continue-on-error: true` in
  `.github/workflows/security.yaml`).
- **Note:** upgrade this to `--audit-level critical` if false-positives become
  a problem, or to `--audit-level moderate` if stricter gating is needed.

### semgrep (SAST scanner)

- **Tool:** `semgrep/semgrep` container image
- **Invocation:** `semgrep scan --config auto --error`
- **Config:** `.semgrep.yaml` in the repo root.
- **Behavior:** `--config auto` loads `.semgrep.yaml` if present; otherwise it
  uses semgrep's registry rules (OWASP Top 10, and so on). `--error` treats
  all findings as errors and exits with non-zero status.
- **Severity:** configurable per rule in `.semgrep.yaml` (see below).
- **Threshold:** failures are reported to the job summary but do not block the
  build in CI (`continue-on-error: true`).

## Severity gates

| Scanner | Gate | In CI |
| --- | --- | --- |
| govulncheck | all vulnerabilities are errors (zero-tolerance) | reported |
| pnpm audit | severity `high` and above | reported (advisory) |
| semgrep | per-rule severity; all findings are errors in local runs | reported (advisory) |

### Raising the bar

To make these checks harder:

1. **pnpm audit.** In the `Makefile`, change:

   ```make
   pnpm audit --audit-level high
   ```

   to:

   ```make
   pnpm audit --audit-level critical
   ```

   Or keep `high` but add `--exit-code 1` to fail the build.

2. **semgrep.** In `.semgrep.yaml`, adjust rule `severity` fields (`ERROR`
   versus `WARNING`) and add more rules.

3. **govulncheck.** Already zero-tolerance; no configuration needed.

## Running locally

```bash
# Run all three
make security

# Run individually
govulncheck ./...
cd frontend && pnpm audit --audit-level high
docker run --rm -v $(CURDIR):/src semgrep/semgrep semgrep scan --config auto --error
```

## CI integration

The `.github/workflows/security.yaml` workflow runs these checks on every
push and PR:

- **govulncheck:** runs in a matrix; results posted to the job summary
  (advisory, does not block).
- **pnpm audit:** in the frontend container; results posted to the job
  summary (advisory).
- **semgrep:** in the semgrep container; results posted to the job summary
  (advisory).

All three are advisory in CI (they report but do not block), allowing the
maintainer to evaluate and decide on remediation. To make one a hard gate,
change `continue-on-error: true` to `continue-on-error: false` in the
workflow.

:::note
This page's source and [Contributing](/ssoossh/project/contributing/) disagree
about semgrep. The scanning source says all three scanners are advisory in CI;
`CONTRIBUTING.md` and `Makefile.md` both say semgrep is a merge gate and only
govulncheck and pnpm audit are advisory. Check
`.github/workflows/security.yaml` for the deployed answer.
:::

## Reconciliation with the CI/CD checks

The `ci-required` Makefile target does **not** include `make security`.
Instead, the `ci-advisory` target runs it:

```make
ci-advisory:
	-$(MAKE) lint
	-$(MAKE) security
```

The leading `-` causes failures to be reported but not to exit the make run
with error status, mirroring CI's `continue-on-error: true` behavior.

To make security a hard requirement before merging, the maintainer can change
the workflow or the Makefile to remove the `-` prefix.

## Future enhancements

- **Dependency update automation:** tools like Dependabot could auto-bump
  versions and run tests.
- **SBOM generation:** for release artifacts, generate a software bill of
  materials.
- **Threshold tuning:** if false-positives accumulate, adjustments to pnpm's
  `--audit-level` or semgrep rule severity can reduce noise.
