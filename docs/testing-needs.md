# Testing needs

Known coverage gaps, each written up with the evidence that it is real —
usually a bug that reached a user through it. This is a worklist, not a
record of what is tested; anything fixed should be deleted from here rather
than marked done.

Entries name the specific property that was not asserted, because "add more
tests" is not actionable and the interesting gaps are rarely a missing file.

## The system config file and `enforce` are not reachable from e2e

**Found:** 2026-08-23, while adding end-to-end coverage of the client's
configuration merge chain.

`config_precedence_test.go` now drives the user file, the local file,
`--config`, and flags against a real binary. Two layers below those are
still proven only by unit tests that call `newConfig` directly with injected
search paths: `/etc/ssoossh/ssoossh.yaml`, and the `enforce` file it can
name.

`defaultSearchPaths` (`client/config/paths.go:39`) hardcodes `/etc/ssoossh`
on every non-Windows platform with no environment override, and that must
stay that way. `enforce` is the administrator's mechanism for locking
settings a user cannot change, so an environment variable relocating the
system directory would be a one-line bypass of the whole control. Adding a
test seam there would be a security regression, not a testing improvement.

What to add: run those cases in a container with a disposable `/etc`. Docker
is available on the development machines and in CI, and tier 3 already does
comparable things to the host (a dedicated account, a real sshd, a PAM
service file), so the precedent exists. The properties worth asserting are
the ones a unit test proves only in-process:

- A system file's `enforce` target beating a user file that sets the same
  key.
- A relative `enforce` target resolving inside the system directory rather
  than the working directory. This is the security property: a
  user-writable `./locked.yaml` must not be picked up.
- A missing or malformed `enforce` file failing closed, naming the file.

## Uncovered error branches with no injection seam

**Found:** 2026-08-23, while restoring client coverage.

Three functions have error paths that no test reaches because there is
nothing to inject a failure through:

- `writeFileAtomic` (`client/cmd/atomicwrite.go:15`, 62.5%) — its write,
  chmod, and close failures. `os.WriteFile` and friends are called directly.
- `server/pubsub.Close` (53.8%) and `newNATS` (0%) — `Router` and
  `GoChannel` are concrete watermill types, not interfaces. The NATS
  constructor is covered end to end by the multi-signer tier, so the unit
  gap overstates the risk.

Deliberately not annotated `not covered:`. Per `.claude/rules/test-go.md`,
that annotation is for a test that genuinely cannot exist, and "awkward to
reach" is a gap to write a test for. The honest fix is a seam — a `writeFile
func(...)` field, or an interface over the transport — which is a design
change worth making on purpose rather than smuggling in behind a test.

## Frontend state modules are largely untested

**Found:** 2026-08-23, on the first frontend coverage measurement.

Frontend coverage is now collected (`pnpm test:coverage`, uploaded by
`resilience.yaml`) and sits at 83.8% of statements. The thin spots are all
client-side state rather than components:

- `lib/branding.svelte.ts` — 0%.
- `lib/session.svelte.ts` — 35.7%.
- `lib/auth.ts` — 66.7%, missing lines 22, 56-64, 77-78.

Components are at 97.2%. No floor is set on the frontend yet; the Go side
has one in `.coverage-floors`, and the same treatment here needs a real
number to ratchet from, which this is.
