#!/usr/bin/env bash
# Guards the two invariants that keep .gitignore from hiding source:
#
#   1. No tracked file matches an ignore rule. A file in that state works
#      for everyone who already has it and is invisible to everyone who
#      does not, so a sibling added later silently never gets committed.
#   2. No source file is ignored. This is the failure that reached CI: a
#      `test*` pattern hid frontend/src/lib/testing/, the suite resolved its
#      imports locally, and a clean checkout could not.
#
# Run from the repo root: scripts/check-gitignore.sh (or `make check-gitignore`).
set -euo pipefail

status=0

# Where authored code lives. Scoping the second check to these keeps it off
# node_modules, which is both enormous and legitimately ignored.
readonly SOURCE_TREES=(cmd client server internal test frontend/src scripts)

# Paths under those trees whose contents are generated or vendored rather
# than authored. They hold files with source extensions, so the check has to
# step around them.
readonly GENERATED='(^server/frontend/dist/|^frontend/src/lib/api/generated/|^test/e2e/_artifacts/|/node_modules/)'

tracked_but_ignored=$(git ls-files -i -c --exclude-standard)
if [ -n "$tracked_but_ignored" ]; then
	echo "These files are tracked but match a .gitignore rule:"
	echo "$tracked_but_ignored" | sed 's/^/  /'
	echo
	echo "Either stop ignoring them (add a negation next to the rule that catches"
	echo "them) or stop tracking them (git rm --cached). Leaving both is how a"
	echo "later sibling file goes missing from a clean checkout."
	echo
	status=1
fi

ignored_source=$(git ls-files -o -i --exclude-standard -- "${SOURCE_TREES[@]}" |
	grep -E '\.(go|ts|js|svelte|css|sql|c|h)$' |
	grep -Ev "$GENERATED" || true)
if [ -n "$ignored_source" ]; then
	echo "These source files are ignored, so a clean checkout will not have them:"
	echo "$ignored_source" | sed 's/^/  /'
	echo
	echo "Find the rule with: git check-ignore -v <path>"
	echo
	status=1
fi

if [ "$status" -eq 0 ]; then
	echo "gitignore: no tracked file matches a rule, no source file is ignored"
fi
exit "$status"
