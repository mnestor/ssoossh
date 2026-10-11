#!/usr/bin/env bash
#
# Create a ready-to-use agent worktree.
#
# Three things trip up `git worktree add` in this devcontainer, every time:
#
#  1. /workspace is root-owned, so git cannot create the leading directories
#     and fails with "could not create leading directories". The directory
#     has to be made and chowned first.
#  2. A failed `git worktree add -b <branch>` still CREATES the branch, so
#     the obvious retry fails again with "a branch named ... already exists".
#     This script checks for the branch and reuses it.
#  3. server/frontend/dist/index.html must exist or ~15 server/bootstrap
#     tests fail with "failed to read index.html". Each worktree needs its
#     own frontend build.
#
# Usage: scripts/new-worktree.sh <name> [base-ref]
#   scripts/new-worktree.sh admin-search          # branch chore/admin-search
#   scripts/new-worktree.sh admin-search main
set -euo pipefail

if [[ $# -lt 1 || "$1" == -h || "$1" == --help ]]; then
    echo "usage: scripts/new-worktree.sh <name> [base-ref]" >&2
    exit 2
fi

name="$1"
base="${2:-main}"
branch="${WORKTREE_BRANCH:-chore/$name}"

# Name the new worktree after the MAIN worktree, not whichever one we happen
# to be standing in. --show-toplevel would give the current worktree, so
# running this from ssoossh-foo would produce ssoossh-foo-bar, and the
# suffixes would compound with every hop. --git-common-dir points at the
# shared .git from every worktree (it can be relative, hence the realpath).
common_dir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
repo_root="$(dirname "$common_dir")"
parent="$(dirname "$repo_root")"
path="$parent/$(basename "$repo_root")-$name"

if [[ -e "$path" ]]; then
    echo "worktree path already exists: $path" >&2
    exit 1
fi

# /workspace is root-owned; make the target ourselves so git only has to
# populate an existing, writable directory.
if ! mkdir -p "$path" 2>/dev/null; then
    echo "creating $path needs sudo (its parent is root-owned)"
    sudo mkdir -p "$path"
    sudo chown "$(id -u):$(id -g)" "$path"
fi

# A previous failed attempt may already have created the branch. Reusing it
# is correct; passing -b again is what fails.
if git show-ref --verify --quiet "refs/heads/$branch"; then
    echo "branch $branch already exists, attaching the worktree to it"
    git worktree add "$path" "$branch"
else
    git worktree add -b "$branch" "$path" "$base"
fi

echo "building the frontend (server/bootstrap tests need dist/index.html)"
( cd "$path/frontend" && CI=true pnpm install --frozen-lockfile >/dev/null && CI=true pnpm build >/dev/null )

# Commits are signed when the git config says so. Sign a throwaway object
# now, so a key that cannot be used is reported before the first commit
# instead of being worked around with --no-gpg-sign, which leaves unsigned
# commits on the branch.
signing="not signed (commit.gpgsign is off)"
if [[ "$(git -C "$path" config --bool --get commit.gpgsign || true)" == true ]]; then
    if git -C "$path" commit-tree -S -m "signing probe" "$(git -C "$path" rev-parse 'HEAD^{tree}')" >/dev/null 2>&1; then
        signing="signed"
    else
        signing="SIGNING FAILS: fix user.signingkey before committing"
        echo "warning: commit.gpgsign is on but a test signature failed; fix the key, do not pass --no-gpg-sign" >&2
    fi
fi

cat <<INFO

worktree ready
  path     $path
  branch   $branch (from $base)
  commits  $signing

  cd $path
  make verify        # lint, lint-tagged, test, check-generated

Notes: every go command needs CGO_ENABLED=1 (the Makefile sets it).
Commits and rebases sign as the git config says; never pass --no-gpg-sign.
INFO
