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

**Found:** 2026-08-23, while restoring client coverage. LDAP addendum
2026-08-29.

Functions with error paths no test reaches because there is nothing to
inject a failure through:

- `writeFileAtomic` (`client/cmd/atomicwrite.go:15`, 62.5%) — its write,
  chmod, and close failures. `os.WriteFile` and friends are called directly.
- `server/pubsub.Close` (53.8%) and `newNATS` (0%) — `Router` and
  `GoChannel` are concrete watermill types, not interfaces. The NATS
  constructor is covered end to end by the multi-signer tier, so the unit
  gap overstates the risk.
- `ldapConnAdapter.Search`/`.Close` and `dialLDAP`'s StartTLS and bind
  branches (`server/service/ldapclient.go`) — thin wrappers over a live
  `*ldap.Conn`. The TLS construction and the fail-before-network branches
  are unit tested; what remains needs a real directory, the same shape of
  gap the NATS constructor has.

Deliberately not annotated `not covered:`. Per `.claude/rules/test-go.md`,
that annotation is for a test that genuinely cannot exist, and "awkward to
reach" is a gap to write a test for. The honest fix is a seam — a `writeFile
func(...)` field, or an interface over the transport — which is a design
change worth making on purpose rather than smuggling in behind a test.

## The frontend has no coverage floor

**Found:** 2026-08-23; scope narrowed and numbers refreshed 2026-08-29.

Frontend coverage is collected (`pnpm test:coverage`, uploaded by
`resilience.yaml`) and sits at 88.1% of statements. The state modules and
admin pages that used to be the thin spots are tested now; what remains
under 80% is:

- `routes/+error.svelte` — 0% (12 statements).
- `routes/certs/[id]/+page.svelte` — 69.9%.
- `routes/logs/me/+page.svelte` — 71.3%.
- `lib/components/BrandMark.svelte` — 66.7%.

The Go side ratchets per package in `.coverage-floors`; the frontend still
has no floor at all, so a slide like the 83.8% → 77.8% one the 2026-08-29
feature merges caused (caught and repaired the same day) fails no gate.
88.1% is the number to ratchet from — vitest's `coverage.thresholds` in
`vite.config.ts` is the mechanism.

## The auth navigation wrappers cannot be observed under jsdom

**Found:** 2026-08-29, while closing the auth module gap.

`startLogin`, `goToLogin`, and the redirect half of
`redirectIfUnauthenticated` (`lib/auth.ts`) end in
`window.location.assign`, which jsdom implements as a logged no-op and
refuses to let a test spy on (`Cannot redefine property: assign`). The
tests call them — the lines execute, and `loginPageURL`'s own cases pin the
URL construction — but the navigation target is asserted nowhere in unit
tests; the e2e browser flows are what actually prove the jumps. Observing
it directly needs a navigation seam or a switch to happy-dom.
