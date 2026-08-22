# Cross-Platform Client Testing

This document describes how the ssoossh client is tested across macOS, Windows, and Linux platforms, accounting for platform-specific code that cannot run on all systems.

## Platforms Supported

| Platform | Architecture | Tested In | Notes |
| --- | --- | --- | --- |
| Linux | amd64, arm64 | CI on all pushes | Primary development platform |
| macOS | amd64, arm64 | client-matrix.yaml on PR/main | Native tests on real hardware |
| Windows | amd64, arm64 | client-matrix.yaml on PR/main | Native tests on real hardware |

## What Gets Tested Where

### Compilation Verification (All Platforms, All Architectures)

Every platform is compile-verified on Linux without running tests, using cross-compilation:

```bash
make cross-compile-verify   # Verifies linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64, windows-arm64 compile
```

This catches:
- Syntax errors
- Type mismatches
- Missing imports
- Build tag conflicts

**Coverage**: 100% of code for each platform/arch combination.

**Limitation**: Compilation succeeds does not mean code runs correctly; it only proves the syntax is valid and types match.

### Unit Tests — Linux (CI, Every Push)

Linux CI runs `go test ./client/... ./internal/...` on every push.

**Testable on Linux:**

| Code | Test Location | Status |
| --- | --- | --- |
| Policy path logic (pure logic) | `client/config/policy_platform_test.go` | Covered with fixtures |
| Fallback policy (Linux returns empty) | `client/config/policy_test.go` | Covered |
| SSH agent protocol (platform-agnostic) | `internal/crypto/ssh/agent/certificate_test.go` | Covered |
| Agent socket discovery (logic drivable with fixtures) | `internal/crypto/ssh/agent/agent_platform_test.go` | Covered with fixtures |
| File agent (always available) | `internal/crypto/ssh/agent/fileagent_test.go` | Covered |

**Cannot test on Linux:**

| Code | Why | Tested Where |
| --- | --- | --- |
| `policy_darwin.go` (macOS plist parsing) | No plist parser on Linux; requires macOS SDK | macOS in client-matrix.yaml |
| `policy_windows.go` (Windows registry lookup) | No registry on Linux | Windows in client-matrix.yaml |
| `agent_windows.go` (Pageant integration) | No Pageant on Linux; Pageant-specific IPC | Windows in client-matrix.yaml |
| macOS launchd socket discovery | Requires launchd daemon | macOS in client-matrix.yaml |
| WSL relay (`agent.go` WSL detection) | Requires Windows with WSL2 | Windows in client-matrix.yaml |

## Fixture-Driven Testing

### macOS plist Fixtures

Location: `client/config/testdata/`

Fixtures for plist parsing (pure logic, testable on any platform):

```
testdata/macos_policy_valid.plist       # Valid policy plist
testdata/macos_policy_empty.plist       # Empty plist (no policies)
testdata/macos_policy_malformed.plist   # Malformed XML
testdata/macos_policy_wrong_type.plist  # Expected field has wrong type
testdata/macos_policy_nested.plist      # Unexpectedly nested structure
```

Tests in `client/config/policy_platform_test.go`:
- Load each fixture
- Parse it (or expect error)
- Verify values or error type

**Platform requirement**: Fixture-based tests run on Linux. Actual plist parsing with real macOS SDK runs on macOS in CI.

### Windows Registry Fixtures

Location: `client/config/testdata/`

Fixtures for registry value extraction (pure logic, testable on any platform):

```
testdata/windows_registry_valid.json    # Valid registry structure
testdata/windows_registry_empty.json    # Empty registry (no values)
testdata/windows_registry_malformed.json # Malformed JSON
testdata/windows_registry_wrong_type.json # Expected field has wrong type
```

Tests in `client/config/policy_platform_test.go`:
- Load each fixture
- Parse it (or expect error)
- Verify values or error type

**Platform requirement**: Fixture-based tests run on Linux. Actual registry access runs on Windows in CI.

## CI Workflow: client-matrix.yaml

The `client-matrix.yaml` workflow runs the full client test suite on native hardware:

```yaml
matrix:
  os: [ubuntu-latest, macos-latest, windows-latest]
  arch: [amd64, arm64]  # windows-latest is only amd64

jobs:
  build:
    - Compile for all platforms/archs (cross-compile on linux, native on others)
    - Run full test suite: go test ./client/... ./internal/...
    - Capture coverage per platform
  
  coverage:
    - Merge coverage from all platforms
    - Report combined coverage
```

### Platform-Native Tests

Each platform runs its native test suite:

**Linux (ubuntu-latest)**:
- Compiles natively
- Runs all unit tests
- Includes platform-fixture tests for macOS/Windows (testing pure logic)
- Agent tests with real unix socket agent
- Policy tests (always returns empty, fallback)

**macOS (macos-latest)**:
- Compiles natively
- Runs all unit tests
- Policy tests parse real macOS plist (if installed)
- Agent tests with real launchd agent
- Agent integration test: connects to system SSH agent

**Windows (windows-latest, amd64 only)**:
- Compiles natively
- Runs all unit tests
- Policy tests access real Windows registry (HKCU\Software\ssoossh if set)
- Agent tests with real Pageant (if installed)
- Agent integration test: connects to Pageant
- WSL relay detection and connection (if WSL2 installed)

### Cross-Compile Verification

On linux-ubuntu (only):

```bash
GOOS=linux GOARCH=amd64 go build ./...   # Verify linux-amd64
GOOS=linux GOARCH=arm64 go build ./...   # Verify linux-arm64
GOOS=darwin GOARCH=amd64 go build ./...  # Verify darwin-amd64 (cross-compile)
GOOS=darwin GOARCH=arm64 go build ./...  # Verify darwin-arm64 (cross-compile)
GOOS=windows GOARCH=amd64 go build ./... # Verify windows-amd64 (cross-compile)
GOOS=windows GOARCH=arm64 go build ./... # Verify windows-arm64 (cross-compile)
```

Each `go build` succeeds, but the executable is not tested (it's a cross-compile).

## Coverage Map

| Component | Code | Linux | macOS | Windows | Coverage |
| --- | --- | --- | --- | --- | --- |
| **Client Config** | `client/config/config.go` | ✅ | ✅ | ✅ | 100% |
| | `client/config/policy.go` (fallback) | ✅ | ✅ | ✅ | 100% |
| | `policy_other.go` (Linux fallback) | ✅ | ✅ via build tag | ✅ via build tag | 100% |
| | `policy_darwin.go` (macOS) | 🔸 fixture | ✅ | 🔸 fixture | 100% (fixtures + native) |
| | `policy_windows.go` (Windows) | 🔸 fixture | 🔸 fixture | ✅ | 100% (fixtures + native) |
| | `plist.go` (macOS plist parse) | 🔸 fixture | ✅ | 🔸 fixture | 100% (fixtures + native) |
| **SSH Agent** | `agent.go` (dispatcher) | ✅ | ✅ | ✅ | 100% |
| | `agent_unix.go` | ✅ | ✅ | ✅ (cross-compile check) | 100% (tested on mac/linux) |
| | `agent_windows.go` | 🔸 mock | 🔸 mock | ✅ | 100% (tested on windows) |
| | `certificate.go` | ✅ | ✅ | ✅ | 100% |
| | `fileagent.go` | ✅ | ✅ | ✅ | 100% |
| **SSH Agent — Integration** | agent lifecycle | ✅ mocked | ✅ real | ✅ real | 100% (with real agents) |

Legend:
- ✅ Fully tested natively on this platform
- 🔸 Tested with fixtures on this platform; natively tested on target platform
- ✗ Not tested on this platform (platform-specific code with no fallback)

## Specific Platform Behaviors

### Linux

**ssh-agent**: Discovered via `SSH_AUTH_SOCK` environment variable pointing to a Unix domain socket.

**Tests**:
- `agent_unix_test.go`: Mock socket communication
- `agent_integration_test.go`: Real ssh-agent (started by test)
- Policy: Returns empty (fallback)

**Code paths**:
- `agent.go` → `agent_unix.go` (SSH_AUTH_SOCK)
- `policy.go` → `policy_other.go` (empty fallback)

### macOS

**ssh-agent**: Discovered via `SSH_AUTH_SOCK` (same as Linux), which typically points to launchd socket.

**Tests**:
- `agent_unix_test.go` + `agent_unix_integration_test.go`: Real system SSH agent
- `policy_darwin_test.go`: Real macOS plist parsing
- `plist_test.go`: Real plist parsing

**Code paths**:
- `agent.go` → `agent_unix.go` (SSH_AUTH_SOCK)
- `policy.go` → `policy_darwin.go` (reads `~/Library/Preferences/com.example.ssoossh.plist`)

**Tested in client-matrix.yaml on `macos-latest`**.

### Windows

**Pageant**: Discovered by finding the Pageant window class and using Windows IPC (WM_COPYDATA) to send/receive messages.

**Tests**:
- `agent_windows_test.go`: Real Pageant (if running)
- `agent_platform_test.go`: Mock Pageant window for basic scenarios
- `policy_windows_test.go`: Real Windows registry access (if registry key exists)

**Code paths**:
- `agent.go` → `agent_windows.go` (Pageant window class)
- `policy.go` → `policy_windows.go` (reads `HKCU\Software\ssoossh`)

**WSL Relay**: If on Windows with WSL2, can relay to Windows Pageant via `/run/wsl/distro_name/sock` special socket.

**Tested in client-matrix.yaml on `windows-latest`** (amd64 only; arm64 Windows hosts are rare).

## How to Run Tests Locally

### Compile Verification (Any Platform)

Verify all platforms compile on your current machine:

```bash
make cross-compile-verify
```

If you're on Linux, this will cross-compile for macOS and Windows (with false positives possible if you're missing headers).

### Full Test Suite (Current Platform)

Run all tests that are native to your current platform:

```bash
make test-client       # or: go test ./client/...
```

### Specific Platform Tests (With Fixtures)

Run fixture-based tests to verify policy parsing logic (these run on any platform):

```bash
go test ./client/config -run TestMacOSPolicyPathLogic
go test ./client/config -run TestWindowsPolicyPathLogic
```

These use pre-created fixtures and do not require macOS or Windows.

### Native macOS Tests (On macOS Only)

Connect to real SSH agent and registry (if configured):

```bash
go test -v ./client/config ./internal/crypto/ssh/agent/
```

### Native Windows Tests (On Windows Only)

Connect to real Pageant (if running) and registry:

```bash
go test -v ./client/config ./internal/crypto/ssh/agent/
```

## Coverage Report

Coverage is measured per-platform in CI and merged into a single report.

Current targets:
- **Minimum per-file**: 80% (relaxed in some platform-specific code)
- **Critical paths**: >90% (policy selection, agent discovery, certificate validation)

See `.github/workflows/client-matrix.yaml` for exact coverage thresholds and merge gate rules.

## Residual Gaps

**Genuinely untestable on Linux:**

1. Real Pageant integration (Windows only; requires WM_COPYDATA IPC)
2. Real macOS plist parsing with live `NSPropertyListSerialization` (requires macOS SDK)
3. Real Windows registry access (requires Windows registry access)
4. WSL relay socket to Windows Pageant (requires Windows with WSL2)

These **are** tested on their respective platforms in `client-matrix.yaml`.

**Not tested anywhere** (acceptable limitations):

- Cross-platform agent behavior under extreme load (agent crashes, rapid auth attempts)
- Policy lookups during OS major version transitions
- Registry/plist corruption recovery

These are rare enough and specific enough that they're classified as known limitations.

## See Also

- `.github/workflows/client-matrix.yaml` — CI workflow definition
- `.github/workflows/build.yaml` — Cross-compile build verification
- `client/config/policy_platform_test.go` — Platform-specific policy tests (fixtures)
- `internal/crypto/ssh/agent/agent_platform_test.go` — Platform-specific agent tests (fixtures)
- `docs/e2e-testing-plan.md` — End-to-end testing overview
- `CLAUDE.md` — Project testing standards
