# Reducing the friction a parallel agent run hits

**Status: acted on 2026-08-25. Six of eight findings are fixed in the tree;
F8 did not reproduce and was dropped.** Findings below are kept as written,
each with a **Resolution** line recording what actually happened. The
`file:line` anchors are as of `5d23809` (2026-08-24) and have already drifted
once — treat them as descriptions, not coordinates.

| | Finding | Outcome |
| --- | --- | --- |
| F1 | Nothing points you at the complete gate | Fixed — `make verify` added, `CONTRIBUTING.md`'s stale `ci-required` list corrected |
| F2 | `lint-tagged` proves compilation, not passing | Fixed — stated in `test/e2e/README.md` |
| F3 | `data-testid` on a component is discarded | Documented — and the premise was wrong: `make frontend-check` already catches it |
| F4 | The coverage ratchet is red on `main` | Fixed — five packages closed with tests, `server/pubsub` floor lowered deliberately |
| F5 | `test-e2e` depends on the bundle existing, not being current | Fixed — `$(FRONTEND_DIST)` now has its sources as prerequisites |
| F6 | `CompleteIdPLogin` returns before its redirects settle | Fixed — all three login helpers now wait |
| F7 | The selector convention is not written down | Fixed — `test/e2e/README.md` |
| F8 | `rtk` serves stale git answers | Not reproducible on rtk 0.45.0; dropped |

## What re-verification found that the original draft did not

Re-running the Provenance checks against `1dad7a0`/`717fe7b` before acting
turned up three things worth recording, because each of them changes what the
right fix was:

- **F4 had got worse, not stayed still.** Three failing floors had become six:
  `server/controller` (74.5% vs 84.0), `server/model` (70.0 vs 74.0) and
  `server/service` (85.4 vs 86.0) had joined the original three, all from
  admin work merged in the interim. A ratchet nobody can act on does not hold
  still while it is ignored — it accumulates.
- **F3's proposed fix would have made things worse.** The premise was that
  Svelte drops the prop silently. It does at runtime, but `svelte-check`
  rejects it at compile time (`'"data-testid"' does not exist in type
  'Props'`) and `make frontend-check` is a blocking merge gate. Spreading rest
  props — the fix the draft preferred — would have made `data-testid` a legal
  unknown prop and *removed* that check. The real cause of the lost afternoon
  was F1: the gate that catches it was never run.
- **A sixth tagged suite existed that nothing compiled.** `LINT_TAGGED` listed
  `e2e resilience load dbparity softhsm`, but `server/pubsub` also has a
  `natsintegration` suite. It was outside every gate — exactly F1's failure
  mode, one tag further along. Adding it to `LINT_TAGGED` immediately turned
  up a real `gosec` finding in `nats_integration_test.go` that had been
  sitting unlinted since the file was written, which is the argument for the
  change in one line.

This came out of running five feature branches in parallel across a dozen
agent sittings. It is not a list of things agents got wrong; it is a list of
places where **the tooling let a wrong answer look like a right one**. Those
are worth fixing regardless of who or what is at the keyboard, because a
human reading the same green output would have drawn the same conclusion.

## What this proposes

Six changes, none large. The theme connecting them: several of our gates
report success for work that was never actually checked, and several of our
conventions are enforced only by knowing them. Both classes cost far more
than they look like they should, because the failure mode is a confident
false negative rather than an error.

## What exists today (verified)

| Thing | Where | State |
| --- | --- | --- |
| Aggregate merge gate | `Makefile:474` (`ci-required`), `Makefile:70` (`pre-pr`) | Correct and complete, `lint-tagged` included |
| Build-tagged lint | `Makefile:266,269` (`LINT_TAGGED`, `lint-tagged`) | Exists, covers e2e/resilience/load/dbparity/softhsm |
| Coverage ratchet | `Makefile:171`, `.coverage-floors` | Three packages below floor on `main` |
| e2e frontend dependency | `Makefile:194` (`test-e2e: $(FRONTEND_DIST)`) | Existence dependency, not freshness |
| Browser login helper | `test/e2e/harness/browser.go:163` | Returns before its redirect chain settles |
| Selector convention | `test/e2e/README.md` | Not documented (0 mentions of `ByQuery`, `querySelector`, `data-testid`) |
| `testid` prop convention | `frontend/DESIGN.md` | Not documented (0 mentions of `testid`) |
| Command rewriting | `.claude/hooks/rtk-rewrite.sh` | Delegates to `rtk rewrite`; rules live in rtk's registry |

## Findings

### F1. The complete gate exists; nothing points you at it

`ci-required` (`Makefile:474`) lists every blocking check, `lint-tagged`
among them, and `pre-pr` runs it. That is right, and it is not the problem.

The problem is that `make test` does **not** build `test/e2e` — the suite is
behind the `e2e` build tag — and `make lint` passes no build tags, so neither
one ever compiles those files. An e2e test can fail to compile entirely while
`make lint`, `make test`, `make check-generated` and every frontend gate
report success. Anyone verifying with a hand-assembled list of targets rather
than `make pre-pr` will believe they have e2e coverage they do not have.

This is not hypothetical: it happened four times in one run, each time with an
honestly-reported green result, and once with a test that did not compile at
all.

*Proposed:* say it in `CONTRIBUTING.md` and `test/e2e/README.md` — verify with
`make pre-pr`, never with a subset — and consider a lighter `make verify`
(lint, lint-tagged, test, check-generated) for the edit loop, so the fast path
still compiles the tagged suites. The value is in `lint-tagged` never being
the target someone leaves out.

*Resolution (2026-08-25):* done. `make verify` (`Makefile`) runs lint,
lint-tagged, test and check-generated for the edit loop, and its comment
explains why `lint-tagged` is the member that cannot be dropped.
`CONTRIBUTING.md`'s "run one piece at a time" list was itself stale — it
omitted `lint-tagged`, `frontend-test`, `cover-floors` and `test-migration`,
so a reader following it got exactly the subset this finding warns about.
Corrected, with the "verify with `pre-pr`, never a subset" rule stated
alongside it.

### F2. `lint-tagged` proves compilation, not passing

Even when run, `lint-tagged` typechecks the tagged suites; it does not execute
them. A green `lint-tagged` alongside no `go test -tags=e2e` run is exactly
what "the e2e tests are fine" looks like when they are not.

*Proposed:* a note in `test/e2e/README.md` stating the distinction in one
line. Cheap, and it removes a genuine ambiguity about what that target means.

*Resolution (2026-08-25):* done, in `test/e2e/README.md`.

### F3. `data-testid` on a component is silently discarded

`Button`, `Card`, `Pager`, `SearchInput` and `Alert` each declare a `testid`
prop and render `data-testid={testid}` themselves. Writing
`<Card data-testid="x">` sets an unknown prop, which Svelte drops without
warning: no error, no console output, the attribute simply never reaches the
DOM and every selector for it fails.

Plain HTML elements take `data-testid=` normally, which is what makes this so
easy to miss — the same spelling works two lines away. It cost roughly an
afternoon of chasing a browser test that looked like a selector bug and was a
dropped prop.

*Proposed:* either have those components spread rest props so both spellings
work, or document the rule at the top of `frontend/DESIGN.md`'s component
section. The first removes the trap; the second only warns about it.

*Resolution (2026-08-25):* documented in `frontend/DESIGN.md` and
`test/e2e/README.md` — **not** with rest props. The premise needs correcting:
the prop is dropped silently at runtime, but `svelte-check` rejects it at
compile time and `make frontend-check` is a blocking merge gate, so the
mistake is already caught by a gate that was simply never run (F1). Spreading
rest props would have made `data-testid` a legal unknown prop and removed
that check, so the documentation explicitly warns against that "fix".

### F4. The coverage ratchet is already red on `main`

`make cover-floors` fails on `main` today:

```
BELOW    server/middleware is at 92.3%, floor is 93.0%
BELOW    server/pubsub is at 56.8%, floor is 66.0%
BELOW    server/utils/errorresponses is at 90.0%, floor is 100.0%
```

A ratchet that is already tripped cannot answer the question it exists to
answer. Anyone who runs it has to be told in advance which three failures to
ignore, which is precisely the instruction that stops being read.

The `errorresponses` floor of 100.0% is worth a second look on its own: a
floor at 100% converts any new defensive branch into a build failure, which
is a strong incentive not to add one.

*Proposed:* close the three gaps, or lower the floors deliberately with the
reasoning in the diff — which is what `.coverage-floors`' own header says the
file is for.

*Resolution (2026-08-25):* closed with tests for five of the six packages
— `server/utils/errorresponses` (75.0 -> 100.0), `server/model` (70.0 ->
100.0), `server/middleware` (92.3 -> 99.0), `server/service` (85.4 -> 86.7)
and `server/controller` (74.5 -> 85.1). The controller and enrollment work
merged in the interim had shipped its authorization paths tested and its
response mapping not, which is most of what the gap was.

`server/pubsub` could not be closed this way and its floor was lowered 66.0
-> 64.0 deliberately, with the reasoning in `.coverage-floors`: roughly a
third of the package is NATS backend code reachable only against a live
broker, and the suite that does that is behind the `natsintegration` tag and
needs Docker, so `go test ./...` never builds it. 64.0 is the true ceiling
for the untagged suite; raising it again means counting `natsintegration` in
the coverage run, not writing more unit tests. `make cover-floors` passes
again, which is the point — the ratchet can now answer the question it
exists to answer.

The `errorresponses` floor of 100.0% was left at 100.0%: the package is
nothing but small error types, and it is back at 100.0%.

### F5. `test-e2e` depends on the frontend existing, not on it being current

`Makefile:194` makes `test-e2e` depend on `$(FRONTEND_DIST)`, which is
`server/frontend/dist/index.html`. That file existing is not the same as it
being built from the current source, and the harness embeds that bundle into
the test server binary. A browser test asserting on markup you just wrote runs
against the markup from whenever the bundle was last built.

The failure presents as a selector timeout, indistinguishable from a wrong
selector. Two branches lost a cycle to it.

*Proposed:* have `test-e2e` rebuild the frontend rather than merely require
it, or have the harness rebuild when the sources are newer than the bundle.

*Resolution (2026-08-25):* done, and more broadly than proposed.
`$(FRONTEND_DIST)` now lists `frontend/src`, `frontend/static` and the build
configs as prerequisites, so the bundle rebuilds whenever its sources are
newer — for every target that depends on it, not just `test-e2e`.

### F6. `CompleteIdPLogin` returns before its redirect chain settles

`test/e2e/harness/browser.go:163` submits the IdP form and returns. The
resulting chain (IdP, callback, application) is still in flight, and chromedp
is not blocking on it. Navigating anywhere at that moment aborts the new
navigation with `net::ERR_ABORTED`.

Every existing test hides this by following the login with a `WaitVisible` on
something from the post-login page. A test that instead navigates somewhere
else — which is the natural shape for "log in, then go to /account" — breaks,
and the error names the destination rather than the race.

*Proposed:* have `CompleteIdPLogin` and `CompleteIdPLoginWithGroups` wait for
the chain to settle before returning. That deletes a whole class of failure
and makes the helper mean what its name says.

*Resolution (2026-08-25):* done. `waitForLoginRedirects` in
`test/e2e/harness/browser.go` blocks until the document has finished loading,
the IdP form is gone, and the location has held still across consecutive
samples; all three helpers call it, including
`CompleteIdPLoginWithExtraClaims`, which has the same race and was not named
in the finding. Verified by reproduction: with the wait disabled, a test that
logs in and navigates straight to `/account` fails on the destination URL;
with it, it passes. `account_test.go`'s hand-rolled `WaitVisible` workaround
was removed, so that test now covers this shape rather than hiding it.

### F7. The selector convention is not written down anywhere

The harness runs `chromedp.WaitVisible(..., chromedp.ByQuery)`, which is
`document.querySelector`: plain CSS only. `:contains()` is a jQuery
extension and `text="..."` is Playwright syntax; both are invalid here and
surface as `DOM Error while querying (-32000)`, which does not obviously mean
"your selector is not CSS".

`test/e2e/README.md` does not mention `ByQuery`, `querySelector` or
`data-testid` at all. The convention is currently transmitted by reading
`approval_test.go` and noticing.

*Proposed:* three lines in `test/e2e/README.md` — CSS only, `data-testid` is
the convention, and F3's prop-versus-attribute rule.

*Resolution (2026-08-25):* done — three points in `test/e2e/README.md`
under "Writing selectors": CSS only, `data-testid` is the convention, and
F3's prop-versus-attribute rule.

### F8. `rtk` serves stale answers for git, and git is where it matters

`.claude/hooks/rtk-rewrite.sh` rewrites commands to their `rtk` equivalents.
For `git status`, `git log` and `git diff` the filtered output has been
observed to be stale: a tree that `/usr/bin/git status --porcelain=v1`
reported as clean and one commit further along came back through the hook as
five modified files at an older HEAD, and `git diff` returned empty for those
same phantom modifications.

Notably `command git` is **not** enough to bypass it; only invoking
`/usr/bin/git` by absolute path was reliable.

Token savings on `git status` are small, and the cost of a wrong answer there
is not: "is this tree clean", "what is HEAD", and "what did this branch
change" are the questions that decide whether work is committed or lost.

*Proposed:* drop `git` from the rewrite rules, or fix the caching. This is the
one finding whose failure mode is losing work rather than losing time.

*Resolution (2026-08-25):* **not reproducible; no change made.** On rtk
0.45.0, `rtk git status --porcelain=v1`, `rtk git log` and `rtk git diff`
agreed with `/usr/bin/git` in every case tried: a clean tree, a tree dirtied
a moment earlier, a reverted tree, and a scratch repository with fresh
commits. The staleness was observed during the original run rather than
reproduced, and the rtk version has moved since.

One part of the finding did hold and is worth keeping: `command git` is
**not** an escape hatch. `rtk rewrite "command git status --porcelain=v1"`
returns `command rtk git status --porcelain=v1` — the rewrite matches through
`command`. `/usr/bin/git` by absolute path is the only reliable bypass. If
the staleness returns, that is the way to confirm it.

## Not proposed

**Anything that makes the gates lenient.** Every finding above is about a gate
being *uninformative*, not about it being too strict. `cover-floors` should be
fixed by closing the gaps or moving the floors on purpose, never by making the
check advisory.

**A house rule that agents must not delete tests.** That was a real incident
in this run — an existing 19-test file removed with a fabricated justification
— but a rule in a document does not prevent it. What catches it is the suite
count moving, which `cover-floors` and `frontend-test` already report when
they are trustworthy. F4 is the fix.

## Sequencing

Ordered by cost-to-value, not dependency; only F1 and F7 are related.

1. **F8 (rtk git)** — highest severity, smallest change, and the only one
   that can cost work rather than time.
2. **F4 (coverage floors)** — restores a gate that currently answers nothing,
   and F4 is what makes a deleted test visible.
3. **F5 (stale bundle)** and **F6 (redirect race)** — two Makefile/harness
   edits that delete two recurring, misleading failure modes.
4. **F3 (testid prop)** — pick the rest-props fix or the documentation fix;
   the first is better.
5. **F1, F2, F7 (documentation)** — a paragraph each in `CONTRIBUTING.md`,
   `test/e2e/README.md` and `frontend/DESIGN.md`.

## Provenance: what was verified and how

Verified against `5d23809` on 2026-08-24:

- **Gate composition**: read `Makefile` lines 70, 254, 266, 269, 474.
  `ci-required` does include `lint-tagged`; an earlier draft of this document
  claimed it did not, which was wrong.
- **Failing floors**: ran `make cover-floors` on `main` with a clean tree; the
  three `BELOW` lines are quoted verbatim from that run.
- **e2e dependency**: read `Makefile:194` and `FRONTEND_DIST` at line 92.
- **Login helper**: read `test/e2e/harness/browser.go:159-176`.
- **Missing documentation**: `grep -c` for `ByQuery|data-testid|querySelector`
  in `test/e2e/README.md` returned 0, and for `testid` in
  `frontend/DESIGN.md` returned 0.
- **Component props**: `grep -l "testid?: string"` over
  `frontend/src/lib/components/*.svelte` returned `Button`, `Pager`, `Card`,
  `SearchInput`, `Alert`.
- **rtk staleness**: observed repeatedly during the run rather than reproduced
  as a minimal case. Before acting on F8, reproduce it directly — compare
  `rtk git status --porcelain=v1` against `/usr/bin/git status --porcelain=v1`
  in a tree with a fresh commit — since the fix differs depending on whether
  the cache is rtk's or the hook's.
