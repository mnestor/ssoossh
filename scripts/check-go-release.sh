#!/usr/bin/env bash
# Reports when the Go toolchain pinned in the CI images is behind the stable
# releases go.dev publishes, and with --file-issue opens a GitHub issue for it.
#
# Why this exists: the pin is `ARG GO_VERSION=` in the two Dockerfiles under
# .github/docker/, fed to a curl download. Dependabot's gomod entry follows
# module versions and its docker entry would follow FROM tags, so neither
# sees that line, and a Go patch release (security fixes included) went
# unnoticed until someone bumped the pin by hand (#254).
#
# scripts/check-go-version.sh is the other half: it checks the pin against
# go.mod. This one checks the pin against upstream.
#
# What it reports, in order of preference:
#   1. a newer patch release on the pinned minor line (1.27.3 -> 1.27.4),
#      which is the one that carries security fixes for the pinned line;
#   2. otherwise a newer minor line (1.27.x -> 1.28.y).
# A newer minor line is also mentioned alongside (1) when both exist.
#
# Usage: scripts/check-go-release.sh [--file-issue]
#   --file-issue  open an issue with `gh` when the pin is behind, unless an
#                 open issue with the same title already exists. Needs
#                 GH_TOKEN with issues: write.
# Environment:
#   GO_DL_JSON    read the release list from this file instead of go.dev
#                 (for testing).
# Exit status: 0 whether or not the pin is behind; non-zero only when the
# check itself could not run.
set -euo pipefail

readonly RUNNER=.github/docker/Dockerfile.runner
readonly RELEASES_URL='https://go.dev/dl/?mode=json'

file_issue=0
case "${1:-}" in
"") ;;
--file-issue) file_issue=1 ;;
*)
	echo "usage: $0 [--file-issue]" >&2
	exit 2
	;;
esac

# True when $1 < $2 under version ordering.
version_lt() {
	[ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$1" ]
}

# The highest version on stdin, one per line; empty when stdin is empty.
highest() {
	sort -V | tail -n 1
}

# The pin. check-go-version.sh already fails CI when the two Dockerfiles
# disagree, so the runner's is the one read here.
pin=$(awk -F= '/^ARG[ \t]+GO_VERSION=/ { print $2; exit }' "$RUNNER")
if ! [[ "$pin" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
	echo "could not read 'ARG GO_VERSION=' from $RUNNER (got '${pin}')" >&2
	exit 1
fi
pin_line=$(echo "$pin" | cut -d. -f1,2)

# Stable releases only, as bare versions ("1.27.4"). go.dev lists the
# supported release lines; anything else in the answer is filtered out so a
# changed or garbled response fails below instead of being compared.
if [ -n "${GO_DL_JSON:-}" ]; then
	releases_json=$(cat "$GO_DL_JSON")
else
	releases_json=$(curl -fsSL --retry 3 "$RELEASES_URL")
fi
stable=$(echo "$releases_json" |
	jq -r '.[] | select(.stable == true) | .version | ltrimstr("go")' |
	grep -E '^[0-9]+\.[0-9]+(\.[0-9]+)?$' || true)
if [ -z "$stable" ]; then
	echo "no stable Go release found in the answer from $RELEASES_URL" >&2
	exit 1
fi

newest=$(echo "$stable" | highest)
newest_on_line=$(echo "$stable" | grep -E "^${pin_line//./\\.}(\.|$)" | highest || true)

# Pick the release to report, per the order in the header.
target=""
if [ -n "$newest_on_line" ] && version_lt "$pin" "$newest_on_line"; then
	target=$newest_on_line
elif version_lt "$pin" "$newest" && [ "$(echo "$newest" | cut -d. -f1,2)" != "$pin_line" ]; then
	target=$newest
fi

if [ -z "$target" ]; then
	echo "Go $pin is current (newest stable: $newest)."
	exit 0
fi

title="Go $target is available (CI images pin $pin)"
body="The runner and devcontainer images pin Go $pin. go.dev lists Go $target as a stable release.

Release history: https://go.dev/doc/devel/release

To move:

- Bump \`ARG GO_VERSION\` in .github/docker/Dockerfile.runner and .github/docker/Dockerfile.devcontainer, together.
- Check that the pinned golangci-lint can read the new toolchain's export data (see the comment above \`GOLANGCI_LINT_VERSION\` in Dockerfile.runner). The build-image PR job runs the merge gates in the new image and shows it.
- Raise the \`go\` directive in go.mod only after build-image has published the new image, or scripts/check-go-version.sh fails in the old one."
if [ "$target" != "$newest" ]; then
	body="$body

Go $newest, a newer release line, is also available."
fi

echo "$title"
echo
echo "$body"

if [ "$file_issue" -ne 1 ]; then
	exit 0
fi

# One issue per target version: an exact title match among open issues means
# an earlier run already reported it.
existing=$(gh issue list --state open --search "\"$title\" in:title" --json number,title --limit 50 |
	jq -r --arg title "$title" '.[] | select(.title == $title) | .number' | head -n 1)
if [ -n "$existing" ]; then
	echo
	echo "Already reported in #$existing; not filing another."
	exit 0
fi

echo
gh issue create --title "$title" --body "$body"
