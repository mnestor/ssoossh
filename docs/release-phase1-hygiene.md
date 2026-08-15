# Phase 1: Repo hygiene and release-blocking debts

**Status: planned.** Part of [release-plan.md](release-plan.md).

## Goal

Get the tree into a state where a tag produces a clean release and a stranger
cloning the repository sees only things that are real.

This is the smallest phase in the plan and it is first because everything
after it assumes a committed set of documents and a working tree with no
debris. None of it is interesting; all of it is cheaper now than after six
more phases have been written on top.

## Work

### 1. Commit the documentation

Most of `docs/` is untracked. `git status` reports `??` for
`admin-authorization-plan.md`, `certificate-keyid-template.md`,
`certificate-lifetime-policy-plan.md`, `e2e-testing-plan.md`, `flows.md`,
`multi-instance-safety-plan.md`, `openapi.yaml`, `project-guidelines.md`,
`signer-split-deferred.md`, `signing-pipeline.md`, `ssoossh-context.md`,
`ssoosshd.yaml.default`, `what-ssoossh-is.md`, and `wire-types.md`, plus
`ideas.md` and the new `release-*.md` files at the root of this plan.

`docs/openapi.yaml` matters most: `make openapi-check` in `build-test.yaml`
compares generated output against a file that is not in the repository, so
the check is asserting against whatever happens to be on the runner's disk.

Note `docs/.gitignore` exists and is itself untracked. Read it before
assuming these are accidental omissions; some may be deliberate.

### 2. Remove working-tree debris

None of these are tracked, so this is `rm`, not `git rm`:

| File | What it is |
| --- | --- |
| `pam_ssoossh.so` (3.3M) | Gitignored. Built from the pre-restructure `cmd/pam_ssoossh/` layout and not reproducible from today's source |
| `pam_ssoossh.h` | Untracked. Generated header from the same stale build |
| `pamtest` (69K), `pamtest.c` | Untracked. Ad-hoc test harness for the old module |
| `server20260808.tgz` (68K) | Untracked archive |
| `a` (0 bytes), `a.sh` | Untracked. `a.sh` is 19 bytes |
| `.DS_Store` (8K) | Should be in `.gitignore` if it is not |

Phase 5 rewrites the module; keeping a binary nobody can rebuild next to
source nobody has compiled is how a stale artifact ends up shipped.

Decide what to do with `pamtest.c` before deleting it. If it encodes
anything about how the module was exercised against a real PAM stack, that
belongs in `pam_ssoossh/` as a test fixture or in the phase 5 notes, not in
an untracked C file at the repository root.

### 3. Strike casbin from the stack

Root `CLAUDE.md` lists casbin among the project's packages. It is not in
`go.mod` and is imported nowhere in live code. The only matches are under
`.claude/worktrees/beautiful-cerf-2e9d14/` and
`.claude/worktrees/focused-pare-df402a/`, which are stale worktree copies of
a pre-restructure layout.

[admin-authorization-plan.md](admin-authorization-plan.md) already reached
this conclusion independently while rejecting casbin as the authorization
mechanism. Remove the line.

While there: decide whether `.claude/worktrees/` should be gitignored. It
currently contains an entire obsolete copy of the application, which is why
a naive `grep` for any symbol returns matches from a layout that has not
existed for months.

### 4. Fix the stray PAM stderr write

`pam_ssoossh/pam_ssoossh.go:34`:

```go
fmt.Fprintf(os.Stderr, "username: %s\n", username)
```

This runs before `initLogger` is called on the next line, so it bypasses the
logger entirely and writes the username to whatever invoked the module.
Under PAM, that is the conversation channel with `sudo`. Delete it, or route
it through `w.Debugf` after the logger exists.

Listed here rather than in phase 5 because it is a one-line fix that does not
need the build environment, and because phase 5 rewrites `Authenticate`, not
`authenticate`.

### 5. Point `docs/README.md` at the release plan

The original delivery plan (`delivery-plan.md` and its ten phase files) has
been removed from the tree rather than kept as history. `docs/README.md`
must not link to it. Confirm the release plan section is the one a reader
lands on, and that nothing else in `docs/` still links to a deleted
`delivery-*.md` file — grep for `delivery-phase` and `delivery-plan.md`
across `docs/` as part of this item, since a stale link is easy to leave
behind when a doc gets rewritten around it.

## Exit criteria

- `git status` is clean apart from work in progress.
- `make openapi-check` compares against a committed `docs/openapi.yaml`.
- No binary artifact in the tree that cannot be rebuilt from source.
- `docs/README.md` points at [release-plan.md](release-plan.md).

## Verification

- `go build ./...` and `go test ./...` still clean with no tags. Nothing here
  should touch behavior except item 4.
- `make openapi-check` and `make types-check` pass from a clean checkout on a
  machine that has never run `make openapi`.
- `git clean -nxd` lists nothing surprising.
