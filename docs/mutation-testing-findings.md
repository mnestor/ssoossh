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

- TTL Enforcement: `docs/deferred.md` "Mutation testing"
- Test Standards: `.claude/rules/test-go.md`
- Prior Finding: Weak test empirically verified and fixed via mutation testing
