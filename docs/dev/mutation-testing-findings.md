# Mutation Testing Findings

Date: 2026-08-22

## Executive Summary

Mutation testing validates test suite assertion strength by introducing intentional code defects and observing whether tests catch them. This document records the methodology, tool survey, executed mutations, and findings for ssoossh.

**Key Finding:** One critical weak test was identified and fixed: `TestCertRequestService_Approve_ShouldRefuseARequestPastTTL` survived removal of the TTL check because it never seeded a user, allowing an earlier "user not found" error to mask the missing TTL enforcement. The fix strengthens both the test (seed user, assert specific error) and validation methodology (empirical mutation testing rather than code review alone).

## Tool Survey

### Go Mutation Testing Tools

No actively maintained Go mutation testing tools exist for Go 1.26+:

- **github.com/gtramontina/ooze**: Library package only, no main/command
- **github.com/zimmski/go-mutesting** (v0.0.0-20210610104036): Library package only, archived upstream
- **Decision:** Manual mutation approach — intentional code modifications coupled with empirical test execution

### Frontend Mutation Testing (Stryker)

Stryker 8.7.1 + vitest runner attempted. **Status: Incompatible with Svelte 5**

**Failures Encountered:**
1. Svelte 5.56.9 compiler no longer exports `walk()` utility expected by Stryker instrumentation
2. Stryker process hung without output when executed, timing out after 180+ seconds
3. Vitest runner plugin resolution (likely fixable with pnpm now available, not pursued further)

**Mitigation:** Stryker is included in frontend/package.json and frontend/stryker.conf.mjs for future attempts as versions advance.

## Mutations Executed: Go (server/service/)

### Mutation 1: TTL Cutoff Check in Approve()

**Location:** `server/service/certrequest.go:378-381`

**Test:** `TestCertRequestService_Approve_ShouldRefuseARequestPastTTL`

**Result:** **SURVIVED (Weak Test - FIXED)**

**Root Cause:** Test never called `seedUser()` before approving, so `bindRequester()` failed with "user not found" first. The test checked `err == nil`, passing because *an* error occurred (just not the TTL error).

**Fix Applied:** Seed user before test, assert on specific error message `"not pending"`.

**Verification:** With fix applied, mutation causes test to fail (correct behavior).

### Mutation 2: Extension Narrowing in narrowRequestedOptions()

**Location:** `server/service/certtypepolicy.go:54`

**Code Modified:** Removed `intersectStrings()` call, used requested.Extensions directly

**Test:** `TestNewCertTypePolicies_ShouldNarrowPAMExtensionsAndUsePAMDuration`

**Result:** **CAUGHT (Strong Test)**

**Analysis:** Correctly validates that client-requested extensions are intersected with server policy. Critical control: server config is the outer bound.

### Mutation 3: Atomic Claim-on-Approve Predicate

**Location:** `server/service/certrequest.go:482`

**Code Modified:** Removed `AND user_id IS NULL` from WHERE clause

**Result:** **SURVIVED (Weak Test - Race Condition Not Tested)**

**Analysis:** The guard prevents concurrent race conditions when two approvers compete for the same unclaimed request. Single-threaded tests cannot expose this. All 29 Approve tests passed because:
1. No concurrent/race test for binding exists
2. Sequential validation (line 472-475) works even with mutation
3. True race requires precise timing of concurrent claims

**Test Gap:** No concurrent race test for `bindRequester()`. Recommend adding similar to existing status-race tests.

## Test Improvements Made

### 1. Strengthen TTL Enforcement Test

**File:** `server/service/certrequest_test.go:1001-1030`

**Changes:**
- Added user seeding before approval
- Changed assertion from bare `err != nil` to specific error message validation
- Added explanatory comment documenting the pattern and fix

**Impact:** Test now catches TTL check removal.

## Recommendations

### Short Term
1. ✓ Fix `TestCertRequestService_Approve_ShouldRefuseARequestPastTTL` — DONE
2. Consider adding concurrent-binding test to catch race condition in bindRequester()
3. Review other permission-flow tests for bare `err == nil` assertions

### Medium Term
1. **Stryker for Frontend:** Revisit when Stryker 10.x or later adds Svelte 5 support
2. **Go Mutation Tooling:** Monitor for new tools; update targets if viable tool emerges
3. **Concurrent Test Harness:** Dedicated suite for race conditions in critical paths

## Configuration

Makefile targets added for mutation testing:

```makefile
mutation-test-frontend: $(FRONTEND_DIST)
	cd frontend && npx stryker run

mutation-test-go:
	@echo "Manual mutation testing focused on critical paths..."

mutation-test: mutation-test-frontend mutation-test-go
```

Frontend config: `frontend/stryker.conf.mjs`

## References

- TTL Enforcement: https://mnestor.github.io/ssoossh/project/decisions/
- Test Standards: `.claude/rules/test-go.md`
- Prior Finding: Weak test empirically verified and fixed via mutation testing

## Critical Finding - Binding Race Condition Test Added

### TestApprove_ShouldRejectDuplicateBindingAttempt

After mutation testing revealed that the binding predicate guard (`WHERE id = ? AND user_id IS NULL`) had zero test coverage, a new test was added to verify the atomic claim-on-approve property.

**Test:** Directly calls `bindRequester` to simulate alice binding first, then verifies bob's binding attempt fails with `ForbiddenError`.

**Mutation Test Results:**
- **With guard present:** Test PASSES (bob's binding correctly rejected)
- **With guard removed** (WHERE "id = ?" only): Test FAILS (bob's binding incorrectly succeeds)

**Critical Property Validated:** Exactly one approver can win the binding race. Without the `WHERE user_id IS NULL` guard, the second approver would overwrite the first's claim, violating the "certificate carries the approver's principals" security model documented at https://mnestor.github.io/ssoossh/concepts/security-model/.

**Limitation Note:** The in-memory SQLite pool uses `SetMaxOpenConns(1)` to prevent "no such table" errors on `:memory:` databases, which serializes all access and masks true race conditions. This test validates the sequential security property. A true concurrent race test would require file-based SQLite and would likely expose additional concurrency issues (e.g., the critical section between read and update is not atomic at the application level). The test documents that the mutation (removing the guard) is immediately caught, proving it serves a real purpose even in the sequential case.

**Recommendation:** If SetMaxOpenConns(1) is ever relaxed for production performance, upgrade the race test to use a temporary file-based database and verify no concurrent writes corrupt the binding.

### Decisions page update

The "Settled: audited and solid, do not rework" section claims:

> Certificate request binding. Atomic claim-on-approve (`UPDATE ... WHERE user_id IS NULL`) prevents two admins contesting ownership.

**Mutation Testing Status:** The code claim is accurate — the guard is in place and correct. However, the coverage claim was NOT accurate — this predicate was not tested by the sequential test suite. The new test (`TestApprove_ShouldRejectDuplicateBindingAttempt`) now provides coverage by proving mutation removal causes failure.

