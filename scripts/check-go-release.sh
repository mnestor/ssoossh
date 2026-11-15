#!/usr/bin/env bash
# Reports when the Go toolchain pinned in the CI images is behind the stable
# releases go.dev publishes. With --file-issue it also opens a GitHub issue
# and pushes a branch that carries the bump, ready for a person to open the
# pull request from.
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
# Why a branch and not a pull request: a pull request opened with the
# workflow's GITHUB_TOKEN does not start other workflows, so its required
# checks would never report. One a person opens from the pushed branch runs
# them as usual.
#
# Usage: scripts/check-go-release.sh [--file-issue]
#   --file-issue  when the pin is behind: open an issue, then push the branch
#                 chore/<issue>-go-<version> with ARG GO_VERSION bumped in
#                 both Dockerfiles. One issue per target version, in any
#                 state: closing it without bumping means "not this one",
#                 and it is not filed again. While the issue is open and the
#                 branch is missing (a failed push, a deleted branch), the
#                 next run pushes it again.
#                 Needs GH_TOKEN with issues: write and contents: write, a
#                 checkout whose origin accepts a push, and a clean tree at
#                 the default branch.
# Environment:
#   GO_DL_JSON    read the release list from this file instead of go.dev
#                 (for testing).
# Exit status: 0 whether or not the pin is behind; non-zero only when the
# check, the issue or the push failed.
set -euo pipefail

readonly RUNNER=.github/docker/Dockerfile.runner
readonly DEVCONTAINER=.github/docker/Dockerfile.devcontainer
readonly RELEASES_URL='https://go.dev/dl/?mode=json'
# The identity GitHub shows as github-actions[bot].
readonly BOT_NAME='github-actions[bot]'
readonly BOT_EMAIL='41898282+github-actions[bot]@users.noreply.github.com'

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

# The Go version a Dockerfile pins.
arg_version() {
	awk -F= '/^ARG[ \t]+GO_VERSION=/ { print $2; exit }' "$1"
}

# Rewrites the ARG GO_VERSION line of Dockerfile $1 to version $2, and fails
# when the file does not end up pinning it. Written through a temporary file
# and back with cat, so the Dockerfile keeps its mode and it works with both
# GNU and BSD sed.
set_arg_version() {
	local file=$1 version=$2 tmp
	tmp=$(mktemp)
	sed -E "s/^(ARG[[:space:]]+GO_VERSION=).*/\\1${version}/" "$file" >"$tmp"
	cat "$tmp" >"$file"
	rm -f "$tmp"
	if [ "$(arg_version "$file")" != "$version" ]; then
		echo "could not set ARG GO_VERSION=$version in $file" >&2
		return 1
	fi
}

# The pin. check-go-version.sh already fails CI when the two Dockerfiles
# disagree, so the runner's is the one read here.
pin=$(arg_version "$RUNNER")
if ! [[ "$pin" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
	echo "could not read 'ARG GO_VERSION=' from $RUNNER (got '${pin}')" >&2
	exit 1
fi
pin_line=$(echo "$pin" | cut -d. -f1,2)

# Stable releases only, as bare versions ("1.27.4"). go.dev lists the
# supported release lines; anything else in the answer is filtered out so a
# changed or garbled response fails below instead of being compared, and so
# nothing but digits and dots reaches a branch name or a sed expression.
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

A branch with \`ARG GO_VERSION\` bumped in .github/docker/Dockerfile.runner and .github/docker/Dockerfile.devcontainer is pushed for this issue; a comment below links it. Open the pull request from it by hand, so the required checks run.

Before merging:

- Check that the pinned golangci-lint can read the new toolchain's export data (see the comment above \`GOLANGCI_LINT_VERSION\` in Dockerfile.runner). The build-image job on the pull request runs the merge gates in the new image and shows it; add the golangci-lint bump to the same branch if it fails.
- Leave the \`go\` directive in go.mod alone until build-image has published the new image, or scripts/check-go-version.sh fails in the old one.

Close this issue without merging to skip Go $target. It is not filed again."
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
echo

# One issue per target version: an exact title match in any state means an
# earlier run already reported it. Only an open one still wants its branch.
match=$(gh issue list --state all --search "\"$title\" in:title" --json number,title,state --limit 50 |
	jq -r --arg title "$title" '[.[] | select(.title == $title)] | sort_by(.number) | last | select(. != null) | "\(.number) \(.state)"')
if [ -n "$match" ]; then
	issue=${match%% *}
	state=${match#* }
	if [ "$state" != "OPEN" ]; then
		echo "Already reported in #$issue, which is closed; nothing to do."
		exit 0
	fi
	echo "Already reported in #$issue."
else
	issue_url=$(gh issue create --title "$title" --body "$body")
	issue=${issue_url##*/}
	if ! [[ "$issue" =~ ^[0-9]+$ ]]; then
		echo "could not read the issue number from '$issue_url'" >&2
		exit 1
	fi
	echo "Filed #$issue."
fi

# The branch name follows the repository rule: <prefix>/<issue>-<slug>.
branch="chore/${issue}-go-${target//./-}"
if [ -n "$(git ls-remote --heads origin "refs/heads/$branch")" ]; then
	echo "Branch $branch already exists; nothing to push."
	exit 0
fi

# Bump both pins on a new branch cut from the checked-out commit, and push.
git switch --quiet --create "$branch"
set_arg_version "$RUNNER" "$target"
set_arg_version "$DEVCONTAINER" "$target"
git add -- "$RUNNER" "$DEVCONTAINER"
git -c "user.name=$BOT_NAME" -c "user.email=$BOT_EMAIL" commit --quiet \
	-m "chore(ci): bump the runner and devcontainer images to Go $target" \
	-m "go.dev lists Go $target as a stable release; the images pinned $pin." \
	-m "Closes #$issue"
git push --quiet origin "refs/heads/$branch"
echo "Pushed $branch."

# Tell the issue where the branch is. The compare link opens the pull
# request form with the branch already chosen.
repo=$(gh repo view --json nameWithOwner,defaultBranchRef --jq '"\(.nameWithOwner) \(.defaultBranchRef.name)"')
gh issue comment "$issue" --body "Branch \`$branch\` carries the bump. Open the pull request: https://github.com/${repo%% *}/compare/${repo#* }...${branch}?expand=1"
