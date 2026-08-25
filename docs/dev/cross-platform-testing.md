# Cross-Platform Client Testing

The client is the only part of ssoossh that runs on machines we do not
operate, so it is the only part with a platform matrix. This describes what
that matrix covers, what it deliberately does not, and the platform
differences a test in `client/` has to be written around.

## Where the tests run

| Leg | Workflow | Runs |
| --- | --- | --- |
| Linux | `codecover.yaml` | `CGO_ENABLED=1 go test ./...` plus the `pam` suite, on every PR. Owns coverage, the Codecov upload, and the `.coverage-floors` ratchet. |
| macOS | `client-matrix.yaml` | `go test -count=1 ./client/... ./internal/crypto/ssh/agent/... ./internal/fileperm/...` on `macos-latest` (darwin/arm64). |
| Windows | `client-matrix.yaml` | The same command on `windows-latest` (windows/amd64). |

`client-matrix.yaml` has no Linux leg and collects no coverage. Both are
deliberate: `codecover.yaml` carries no path gate at all and so runs the
whole unit suite on every PR, which makes a Linux leg here a second run of
something that already ran; and merging partial per-OS profiles would
double-count or false-fail on files behind build tags. It also has no
goreleaser job -- `build.yaml` owns release-pipeline validation.

Static analysis of the other two builds does not need their machines.
`lint.yaml` runs `make lint-cross`, a golangci-lint pass over the `windows`
and `darwin` builds of the same three package trees, on the same ubuntu
container as every other lint job. It is a merge gate, and it covers ground
the matrix never will: a lint finding is not a test failure, so a suite can
be green on Windows with an integer-overflow bug sitting in a file only that
leg compiles. That is exactly what happened to `policy_windows.go`.

The matrix is path-gated on `client/**`, `cmd/ssoossh/**`, `internal/**`,
`go.mod`, `go.sum`, and the workflow file. macOS minutes bill at ten times
the Linux rate, so a PR that touches none of those skips both legs, and a
skipped job still satisfies a required check.

### Why those three package trees

One unfiltered run per OS is the whole job, so the list is short on purpose:

- `./client/...` is everything that only ever runs on a user's machine.
- `./internal/crypto/ssh/agent/...` is the agent, whose Windows half talks
  to Pageant and whose Unix half talks to a socket. Neither can stand in for
  the other.
- `./internal/fileperm/...` is the only thing keeping a private key away
  from every other account on a Windows box, and the Linux leg cannot
  compile that file at all, let alone check the access list it writes.

## Tests that exist on one platform only

These are behind build tags, so the Linux leg never compiles them and their
lines never appear in a coverage profile. They are covered where they run,
not excluded.

| File | Tag | What it needs |
| --- | --- | --- |
| `client/config/policy_darwin_test.go` | `darwin` | Real plist parsing, with `managedPreferencesDir` pointed at a temp directory so the test needs no MDM enrollment. |
| `client/config/policy_windows_test.go` | `windows` | A real registry key, created under `HKCU\Software\ssoossh-policy-test\` and deleted on cleanup, so no administrator rights are needed. |
| `internal/fileperm/fileperm_windows_test.go` | `windows` | Reads the DACL back off a file to prove it names the three intended trustees and no longer inherits. |
| `internal/crypto/ssh/agent/agent_unix_test.go` | the Unix GOOS list | A Unix domain socket for `SSH_AUTH_SOCK`. |

`internal/crypto/ssh/agent/agent_integration_test.go` is behind
`//go:build integration` and is not part of any of the three legs.

## Platform differences a client test has to survive

Every one of these has already turned a green Linux run into a red Windows
one. They are properties of the platform, not of the code under test.

**File modes are not access control on Windows.** `os.Chmod` there writes
one bit, the read-only attribute. Go reports every writable file as `0666`
whatever mode it was created with, so `want 0600` fails on Windows for a
file that was written correctly. Use `wantPerm` (`client/cmd/permissions_test.go`)
rather than a bare octal literal. The real protection is
`internal/fileperm`, which writes an explicit access list; assert on that
when what you mean is "only the owner can read this".

**`os.Chmod` cannot take a read away.** A `0000` file still reads back fine
on Windows, so a test that needs an unreadable file has to skip there. See
`TestRunHostPrincipals_ShouldFailWhenTheMappingCannotBeRead`.

**`os.IsNotExist` swallows more than it does on Unix.** A path under a
regular file gives `ENOTDIR` on Unix but `ERROR_PATH_NOT_FOUND` on Windows,
which `os.IsNotExist` reports as missing. To provoke a stat error that is
reliably neither "exists" nor "missing", put a NUL byte in the path: that
fails validation before any syscall on every platform.

**`filepath.IsAbs("/dev/null")` is false on Windows.** A leading slash is
not a volume name, so a Unix absolute path used as a fixture resolves
somewhere under the user profile and quietly works. `/dev/null` as a
"guaranteed unwritable path" passed on Windows and let a test's forbidden
network call go out.

**`%q` escapes a Windows path.** `strings.Contains(err.Error(), path)`
against an error formatted with `%q` matches on Unix and never on Windows,
because `C:\Users\...` comes back as `C:\\Users\\...`. Compare against
`strconv.Quote(path)`.

**Git rewrites line endings on checkout.** Git for Windows defaults to
`core.autocrlf=true`, which turns a golden file into CRLF while the value
the code renders stays LF. The diff then prints identically on both sides.
`.gitattributes` pins `*.golden` to `eol=lf`; `test/configgolden` says so
outright if it ever happens again.

## What cross-compilation is and is not verified

`build.yaml`'s goreleaser jobs build all six shipped targets, but
`build-most` does not run on pull requests. On a PR the compile coverage is
what the three legs give natively:

| Target | Compiled on a PR |
| --- | --- |
| linux/amd64 | Yes, by `codecover.yaml` and `build.yaml`'s single-target check |
| darwin/arm64 | Yes, by the macOS leg |
| windows/amd64 | Yes, by the Windows leg |
| linux/arm64, darwin/amd64, windows/arm64 | No, only on push, tag, and schedule |

The three that miss a PR differ from a covered target only by architecture,
never by GOOS, so a build tag or a platform API cannot break in one without
breaking the leg beside it. Nothing here needs another matrix axis.

## Running it locally

```bash
make lint-cross                         # golangci-lint the windows and darwin builds
make test-client                        # the client suite for your platform
go test ./internal/fileperm/            # the package the matrix adds
GOOS=windows go test -c -o /dev/null ./client/cmd   # one package, without the lint pass
```

`make lint-cross` is the one to reach for. `make lint` runs with your own
GOOS, so it never sees a file behind a `windows` or `darwin` constraint;
`lint-cross` runs golangci-lint over both cross builds and is a merge gate
in `lint.yaml`. It needs no Windows or macOS machine, because typechecking a
cross build does not run anything. It is scoped to the same three package
trees the matrix tests, since those hold every platform-constrained file in
the repo.

What that still cannot tell you is whether the code behaves. Compiling and
linting the Windows build proves nothing about the mode a file comes back
with or what `os.IsNotExist` decides. Only the matrix catches the
differences listed above.

## Known gaps

- Pageant (`internal/crypto/ssh/agent/agent_windows.go`) has no
  `//go:build windows` test. The Windows leg compiles it and nothing
  exercises it; the hosted runner has no Pageant to talk to.
- `TestWindowsRegistryFixture` and `TestRegistryFixtureJSON` in
  `client/config/policy_platform_test.go` build a registry-shaped literal
  and assert on that literal. They call no product code and prove nothing
  about `policy_windows.go`, which is covered by
  `policy_windows_test.go` on the Windows leg instead.
- The WSL relay path needs a Windows host with WSL2 and is not exercised
  anywhere.

## See also

- `.github/workflows/client-matrix.yaml` -- the macOS and Windows test legs
- `.github/workflows/lint.yaml` -- the cross-GOOS lint gate (`make lint-cross`)
- `.github/workflows/codecover.yaml` -- the Linux leg, coverage, and the floors
- `client/cmd/permissions_test.go` -- `wantPerm`
- `internal/fileperm/` -- what `0600` means on Windows
