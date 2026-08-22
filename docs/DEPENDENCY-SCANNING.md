# Security and Dependency Scanning

The `make security` target runs three complementary scanners to detect vulnerabilities and insecure patterns.

## Configuration

### govulncheck (Go Vulnerability Checker)

**Tool:** `golang.org/x/vuln/cmd/govulncheck`  
**Invocation:** `govulncheck ./...`  
**Config:** None — this tool has no config file. It checks the Go standard vulnerability database for known CVEs in your dependencies.  
**Severity:** Fails the build if any vulnerabilities are found (all are treated as errors).  
**Threshold:** Zero-tolerance — any detected vulnerability blocks the build.

### pnpm audit (JavaScript/Node Vulnerability Checker)

**Tool:** `pnpm audit`  
**Location:** `frontend/` directory  
**Invocation:** `pnpm audit --audit-level high`  
**Config:** The `--audit-level high` flag filters to high and critical severity vulnerabilities.  
**Severity:** `high` and `critical` (low and moderate warnings are informational only).  
**Threshold:** Failures are reported to the job summary but do not block the build in CI (`continue-on-error: true` in `.github/workflows/security.yaml`).  
**Note:** Upgrade this to `--audit-level critical` if false-positives become a problem, or to `--audit-level moderate` if stricter gating is needed.

### semgrep (SAST Scanner)

**Tool:** `semgrep/semgrep` container image  
**Invocation:** `semgrep scan --config auto --error`  
**Config:** `.semgrep.yaml` in the repo root.  
**Behavior:**  
- `--config auto` loads `.semgrep.yaml` if present; otherwise uses semgrep's registry rules (OWASP Top 10, etc.).
- `--error` treats all findings as errors and exits with non-zero status.

**Severity:** Configurable per rule in `.semgrep.yaml` (see below).  
**Threshold:** Failures are reported to the job summary but do not block the build in CI (`continue-on-error: true`).

## Severity Gates

### Explicit Gates

- **govulncheck:** All vulnerabilities → error (zero-tolerance)
- **pnpm audit:** Severity >= `high` → reported (advisory in CI)
- **semgrep:** Severity per rule → reported (advisory in CI), all findings are errors in local runs

### Raising the Bar

To make these checks harder:

1. **pnpm audit:**  
   In `Makefile`, change:
   ```make
   pnpm audit --audit-level high
   ```
   to:
   ```make
   pnpm audit --audit-level critical
   ```
   Or keep `high` but add `--exit-code 1` to fail the build.

2. **semgrep:**  
   In `.semgrep.yaml`, adjust rule `severity` fields (`ERROR` vs `WARNING`) and add more rules.

3. **govulncheck:**  
   Already zero-tolerance; no configuration needed.

## Running Locally

```bash
# Run all three
make security

# Run individually
govulncheck ./...
cd frontend && pnpm audit --audit-level high
docker run --rm -v $(CURDIR):/src semgrep/semgrep semgrep scan --config auto --error
```

## CI Integration

The `.github/workflows/security.yaml` workflow runs these checks on every push and PR:

- **govulncheck:** Runs in a matrix; results posted to job summary (advisory, does not block).
- **pnpm audit:** In frontend container; results posted to job summary (advisory).
- **semgrep:** In semgrep container; results posted to job summary (advisory).

All three are advisory in CI (they report but do not block), allowing the maintainer to evaluate and decide on remediation. To make one a hard gate, change `continue-on-error: true` to `continue-on-error: false` in the workflow.

## Reconciliation with CI/CD Checks

The `ci-required` Makefile target does **not** include `make security`. Instead, the `ci-advisory` target runs it:

```make
ci-advisory:
	-$(MAKE) lint
	-$(MAKE) security
```

The leading `-` causes failures to be reported but not to exit the make run with error status, mirroring CI's `continue-on-error: true` behavior.

To make security a hard requirement before merging, the maintainer can change the workflow or the Makefile to remove the `-` prefix.

## Future Enhancements

- **Dependency update automation:** Tools like Dependabot could auto-bump versions and run tests.
- **SBOM generation:** For release artifacts, generate a Software Bill of Materials.
- **Threshold tuning:** If false-positives accumulate, adjustments to pnpm's `--audit-level` or semgrep rule severity can reduce noise.
