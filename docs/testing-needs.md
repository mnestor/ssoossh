# Testing needs

Known coverage gaps, each written up with the evidence that it is real —
usually a bug that reached a user through it. This is a worklist, not a
record of what is tested; anything fixed should be deleted from here rather
than marked done.

Entries name the specific property that was not asserted, because "add more
tests" is not actionable and the interesting gaps are rarely a missing file.

## e2e never exercises file-based key storage

**Found:** 2026-08-23, after a file-backed `ssh login` deleted the key files
it had just written and reported success (fixed in `800d5e1`).

Every e2e test runs against a real ssh-agent. `harness.StartAgent`
(`test/e2e/harness/agent.go:33`) launches `ssh-agent -D -a <socket>` and
`harness/client.go:48,163` put `SSH_AUTH_SOCK` in the environment of every
client invocation. Nothing under `test/e2e/` sets `use_agent: false` or
drives `fallback_file_agent`, so the file-agent path has no end-to-end
coverage at all.

That is what let the bug ship. The agent path was fine — a real ssh-agent's
`List` returns certificates, so `ssh login`'s `pruneSuperseded` matched the
identity it had just installed and left it alone. Only the file path, the
one never driven, was broken.

What to add:

- A `use_agent: false` variant of `TestLogin_ApprovingDeliversCertificateOverSSE`
  asserting the private key, public key, and certificate all exist on disk
  after the login. Fits tier 1.
- A `use_agent: false` variant of `TestSSH_SshdAcceptsTheIssuedCertificate`.
  Needs tier 3. This matters more than it looks: file mode is the documented
  `Match exec` story for hosts without an agent, and nothing currently proves
  an sshd will accept a certificate obtained that way.

Parameterizing the whole tier over both backends was considered and
deliberately deferred — the failure mode here is "does file mode work at
all", not "do the two backends behave identically" — but it is worth
revisiting once file mode is known good.

## Unit tests that count results without checking their type

**Found:** 2026-08-23, same bug as above.

`TestFileAgent_List` asserted only `len(got) != 1`. A bare public key and a
certificate both satisfy that, so the test passed while `List(true)`
returned the wrong kind of thing entirely — which is what
`pruneSuperseded` and `ssh inspect` both depend on. `ssh_inspect.go:47`
casts the result to `*ssh.Certificate` and has always carried a comment
saying the failure branch is "unreachable short of a backend bug"; the
backend bug was real and untested.

The type assertion is now there. Worth a sweep for the same shape elsewhere:
a test whose only assertion is a length or a count, over a collection whose
element type or identity is the thing that actually matters.

## Approval-page frontend tests have never been executed

**Found:** 2026-08-23, while merging the multi-principal user certificate
work.

`frontend/src/lib/components/ApprovalView.test.ts` and
`frontend/src/routes/approve/[id]/page.test.ts` gained coverage for the
principal picker, but no environment available at the time had node, so the
suite has never run. They are written against the existing patterns in those
files and reviewed by hand, which is not the same as passing. Run them
before trusting them.

Related: `make openapi-lint` shells out to `npx @redocly/cli`, so it is
skipped in the same environments for the same reason.

## The client man page is hand-written fiction, and its gate is vacuous

**Found:** 2026-08-23, after adding a `--verbose` flag that never appeared in
`docs/man/ssoossh.1` even though `make gendocs` reported success and
`make man-check` passed.

`generateClientManpage` (`internal/tools/gendocs/main.go:105`) does not
generate from the client's command tree. It hand-builds a parallel cobra
command: the Short and Long strings are copy-pasted from
`RootCommand.Init`, `--config` and `--server` are re-declared by hand, and
the subcommands are five stubs with invented descriptions. The server page
above it is generated from the real `servercmd.NewCommand()`; only the
client page is a duplicate.

So the page drifts silently, and it already has:

- `host` is described as "Manage host certificates". The real command is
  "Manage local sshd principal mapping", and `docs/decisions.md:61` records
  that host certificates were removed. The man page documents a feature that
  does not exist.
- `ca` is described as "Manage CA certificates"; it prints the CA public key.
- `--verbose` is absent, as is every subcommand below the top level —
  `ssh login|logout|proxycommand|inspect|config`, `host principals|mapping`,
  `service enroll|retrieve` — and all of their flags.

`make man-check` cannot catch any of this. It regenerates with gendocs and
diffs the result against the committed file, so it compares gendocs to
itself. It is a real gate for the server page and a vacuous one for the
client page — worse than no gate, because it reports success.

The fix is to generate from the real tree. The obstacle named in the comment
is real but not insurmountable: `simplecobra.New` returns an `*Exec` that
exposes only `Execute`, so the assembled `*cobra.Command` is not directly
reachable. `Execute` returns a `*Commandeer`, which does carry
`CobraCommand`, so one route is to execute the tree once with output
redirected to `io.Discard` purely to obtain it. Another is for `client/cmd`
to expose an accessor that assembles the same `[]simplecobra.Commander` and
hands back the cobra root. Either beats maintaining a second copy of the CLI
by hand.

Until then, treat `docs/man/ssoossh.1` as unverified: adding a flag or a
subcommand does not update it, and nothing will tell you.
