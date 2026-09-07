# Running Multiple AI Agents in Parallel

This guide explains how multiple AI agents (e.g., different Claude Code sessions) can work on separate features simultaneously without corrupting the repository.

## Quick Rules

1. **One worktree per agent.** Each agent gets its own git worktree so they don't interfere with each other's file state.
2. **Schema is single-owner.** Only one agent at a time can edit `server/model/` or `server/resources/migrations/` (the database schema).
3. **Coverage is reported unfiltered.** There is no exclusion list to maintain: a block that cannot be tested carries a `not covered:` comment in place, and `.coverage-floors` ratchets each package's floor. Another agent's edits cannot invalidate either.
4. **No parallel `make test-e2e`.** The end-to-end test suite uses fixed host resources (a local user account, sshd on fixed ports); run it serially.
5. **After merging, regenerate.** When a branch lands, the integrator runs `make check-generated` (or the full `make pre-pr`) to catch stale generated files: types, openapi, man pages, confdocs, clidocs, Makefile docs, and the wire contract.

## Detailed Setup

### Creating a New Agent Worktree

```bash
# On main, commit or stash any untracked work
git status
git stash -u  # if needed

# Create a new worktree for the agent (wraps `git worktree add`; see Makefile.md)
make worktree NAME=<feature>            # BASE=main, WORKTREE_BRANCH=feat/<feature> by default
cd ../ssoossh-<feature>
```

The agent now has an independent checkout and index, shared object store with main.

### Assigning Ownership

Before two agents start, assign:

| Resource | Assigned To | Reason |
| --- | --- | --- |
| `server/model/` + `server/resources/migrations/` | [Agent Name] | Two branches each adding a numbered migration per dialect will collide on ordering; the schema is one sequence. |

### Parallel Work Phases

**Phase 1: Independent work** (all agents in their own worktrees)
- Each agent commits to their own branch.
- Run `make test`, `make lint`, `make cover` in your worktree; they are isolated.
- `server/frontend/dist` is built per worktree (gitignored) so Go tests compile without conflict.

**Phase 2: Merging** (serial, one branch at a time)

When a branch is ready to merge:

1. The branch owner rebases onto the latest `main`:
   ```bash
   git fetch origin
   git rebase origin/main
   ```

2. Push the branch and open a PR. CI runs all checks.

3. Once CI passes, merge the branch.

4. **The integrator** (not the agent) runs the post-merge ritual **immediately**:
   ```bash
   cd <main-worktree>
   git pull
   
   # Regenerate and verify every generated artifact (types, openapi, man,
   # confdocs, clidocs, Makefile docs, wire contract)
   go mod tidy
   make check-generated
   
   # Verify the gate passes
   make lint && make test
   
   # If changes were made, commit them
   git add .
   git commit -m "chore: regenerate after <feature> merge"
   ```

5. The next branch then rebases onto this updated `main` before merging.

**Do NOT** skip step 4. If you merge two agent branches without regenerating between them:

- Generated files (openapi.yaml, TypeScript types, man pages, the docs-site references, the wire contract) will collide and produce wrong output.

## What Merges Safely in Parallel

These can be edited by multiple branches without conflict:

- **Controller handler code** — separate endpoints (different POST/GET paths).
- **Test files** — separate test packages.
- **Frontend components** — different `.svelte` files.
- **Client/server logic** — separate packages (e.g., `server/signer/` and `server/service/` can both evolve).

Merge **serially if**:

- Both branches touch `server/model/` or `server/resources/migrations/`.
- The same frontend component is being rewritten.

## Central Collision Points

Features that add routes or services will conflict here (both sides keep their additions):

- `server/bootstrap/router.go` — route registration
- `server/bootstrap/bootstrap.go` — service construction
- `server/model/model.go` — model list

These conflicts are **mechanical:** take both sides, run `make check-generated`, and commit.

## Cleanup

When a branch is merged and the worktree is no longer needed:

```bash
cd ..
git worktree remove ssoossh-<feature>
```

## Why This Discipline Matters

Two things merge cleanly but are wrong afterwards:

1. **Generated files** — The merger doesn't know that `openapi.yaml` is derived from Go annotations. Two branches both regenerate, outputs collide, and the merged result matches neither.
2. **Migrations** — Two schema change branches each add a numbered migration. No merge marker because both are "additive" (new files). But if one adds a column named `status` and the other adds a constraint on `status`, the order matters. Silent drift.

The post-merge ritual catches the first; serial schema ownership prevents the second.

## Worked Example

**Agent A** is adding a new approval flow feature (new service, new routes).  
**Agent B** is adding a new logging middleware.

1. A creates worktree `ssoossh-approval-flow`, B creates `ssoossh-logging-middleware`.
2. A owns `server/bootstrap/bootstrap.go` (service registration); B owns new middleware code.
3. Both work in parallel in their worktrees.
4. B's branch lands first. The integrator runs the post-merge ritual.
5. A rebases onto updated `main`, conflicts in `server/bootstrap/bootstrap.go` are mechanical (both sides' service registration kept), rebase succeeds.
6. A's PR opens, CI passes, merges. The integrator runs the post-merge ritual again.

