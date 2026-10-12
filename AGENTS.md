# Running Multiple AI Agents in Parallel

This guide explains how several AI agents (for example, separate Claude Code sessions) can work on separate features at the same time without corrupting the repository.

## Quick Rules

1. **One worktree per agent.** Each agent works in its own git worktree. The main checkout is shared: an agent working there never switches its branch, and every agent runs `git branch --show-current` before it commits, because `git switch` carries uncommitted changes onto whatever branch it lands on.
2. **Schema is single-owner.** Only one agent at a time edits `server/model/` or `server/resources/migrations/` (the database schema).
3. **Coverage is reported unfiltered.** There is no exclusion list to maintain: a block that cannot be tested carries a `not covered:` comment in place, and `.coverage-floors` ratchets each package's floor.
4. **`make test-e2e` runs one at a time.** It takes an flock on `/tmp/ssoossh-e2e.lock`, so a second run waits instead of interfering. The suite changes host state (the `ssoossh-e2e` account, `sshd` under sudo); read `test/e2e/README.md` first, and use `make test-e2e-unlocked` only for a deliberate parallel run.
5. **Regenerate after every update from main.** After merging or rebasing `origin/main` into a branch, run `make check-generated` before pushing. tygo copies Go doc comments into the generated TypeScript, so even a comment-only Go change can leave an artifact stale.

## Detailed Setup

### Creating a New Agent Worktree

Branch names carry the issue number (`.claude/rules/git.md`), and the worktree uses the same `<issue>-<slug>`:

```bash
git fetch origin
make worktree NAME=172-load-more-focus BASE=origin/main WORKTREE_BRANCH=fix/172-load-more-focus
cd ../ssoossh-172-load-more-focus
```

`make worktree` wraps `git worktree add` (see `Makefile.md`). Without `WORKTREE_BRANCH` the branch is `chore/<NAME>`, and `BASE` defaults to the local `main`, which can lag `origin/main`. The script builds the frontend into the new worktree and checks that commit signing works. Commits are signed as the git config says; never pass `--no-gpg-sign`.

The worktree has its own checkout and index and shares the object store with the main checkout.

### Assigning Ownership

Before two agents start, assign:

| Resource | Assigned To | Reason |
| --- | --- | --- |
| `server/model/` + `server/resources/migrations/` | [Agent Name] | Each schema change adds a timestamped migration to both `postgres/` and `sqlite/`. golang-migrate only applies versions above the highest one a database has recorded, so a migration that merges after a newer-timestamped one never runs on a database that already took the newer one. |

### Parallel Work Phases

**Phase 1: Independent work** (all agents in their own worktrees)
- Each agent commits to its own branch.
- Run `make verify` in the edit loop. Besides `lint`, `test` and `check-generated`, it lints the build-tagged suites and the Windows and macOS builds, which a hand-picked `make lint test` misses.
- `server/frontend/dist` is per worktree (gitignored). `make worktree` builds it, and `make test` rebuilds it when it is missing or stale.

**Phase 2: Merging** (one branch at a time)

`main` only accepts pull requests, and a PR merges only when every required check passes and the branch is up to date with `main`. To get a branch there:

1. Bring it up to date: `git fetch origin`, then merge `origin/main` into it (or rebase, if the branch has not been pushed).
2. Run `make check-generated`, then `make pre-pr`, the full merge gate. Never verify with a subset.
3. Push and open the PR.
4. When another PR lands first, repeat steps 1 and 2.

Nothing is regenerated on `main` after a merge. The up-to-date requirement means every branch is checked against the `main` it will become part of.

## What Merges Safely in Parallel

These can be edited by several branches without conflict:

- **Controller handler code**: separate endpoints (different POST/GET paths).
- **Test files**: separate test packages.
- **Frontend components**: different `.svelte` files.
- **Client/server logic**: separate packages (for example, `server/signer/` and `server/service/` can both evolve).

Merge **serially if**:

- Both branches touch `server/model/` or `server/resources/migrations/`.
- The same frontend component is being rewritten.

## Central Collision Points

Features that add routes, services or settings conflict here (both sides keep their additions):

- `server/bootstrap/router.go`: route registration
- `server/bootstrap/bootstrap.go`: service construction
- `server/config/defaults.yaml`: one entry per config key
- `frontend/DESIGN.md`: design tokens, which most UI work touches

These conflicts are **mechanical**: take both sides, run `make check-generated`, and commit.

## Cleanup

When a branch is merged and the worktree is no longer needed:

```bash
cd ..
git worktree remove ssoossh-<issue>-<slug>
git -C ssoossh branch -d <prefix>/<issue>-<slug>
```

## Why This Discipline Matters

Two things merge cleanly but are wrong afterwards:

1. **Generated files.** `docs/openapi.yaml` comes from the swag annotations, the TypeScript types from the Go declarations, the man pages and docs-site references from the cobra commands and config structs. A conflict in one is never resolved by hand: take either side and regenerate.
2. **Migrations.** Two schema branches each add a migration. There is no conflict marker because both are new files, but golang-migrate applies them by version, so merge order and timestamps have to agree. Silent drift.

Regenerating after every update catches the first; serial schema ownership prevents the second.

## Worked Example

**Agent A** is adding a new approval flow (new service, new routes) for issue 201.
**Agent B** is adding a logging middleware for issue 202.

1. A creates worktree `ssoossh-201-approval-flow` on `feat/201-approval-flow`; B creates `ssoossh-202-logging-middleware` on `feat/202-logging-middleware`.
2. A owns `server/bootstrap/bootstrap.go` (service registration); B owns the new middleware code.
3. Both work in parallel in their worktrees.
4. B's PR merges first.
5. A merges `origin/main` into its branch. The conflict in `server/bootstrap/bootstrap.go` is mechanical (both sides' registrations kept). A runs `make check-generated` and `make pre-pr`, pushes, and its PR merges once CI passes.
