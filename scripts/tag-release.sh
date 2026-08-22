#!/usr/bin/env bash
# Computes the next semver tag with `svu next` (conventional commits since
# the last tag), then creates and pushes an annotated tag — which is what
# actually kicks off release.yml. Prompts for confirmation before pushing
# since that's the hard-to-reverse step.
set -euo pipefail

# Prompts for confirmation, then creates and pushes an annotated tag.
# force=1 moves an existing tag (-fa / --force); otherwise it's a plain new tag.
confirm_and_push_tag() {
	local tag=$1 prompt=$2 verb=$3 force=$4

	read -rp "${prompt} [y/N] " CONFIRM
	if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
		echo "Aborted."
		exit 1
	fi

	if [ "$force" = 1 ]; then
		git tag -fa "${tag}" -m "${tag}"
		git push origin "${tag}" --force
	else
		git tag -a "${tag}" -m "${tag}"
		git push origin "${tag}"
	fi

	echo "${verb} ${tag} — release.yml will pick it up from here."
}

LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "<none>")
NEXT=$(svu next)

# svu next lands on v0.0.0 when there's no prior tag and no commit yet
# triggers a bump (e.g. a repo's first commit isn't Conventional-Commits
# formatted) — that's not a real, taggable version, so treat it as a
# first release and bump the minor instead.
if [ "$NEXT" = "v0.0.0" ]; then
	NEXT=$(svu minor)
fi

if [ "$NEXT" = "$LAST_TAG" ]; then
	TAG_COMMIT=$(git rev-list -n1 "$LAST_TAG")
	HEAD_COMMIT=$(git rev-parse HEAD)

	if [ "$TAG_COMMIT" = "$HEAD_COMMIT" ]; then
		echo "No commits since ${LAST_TAG}; nothing to release." >&2
		exit 1
	fi

	# HEAD moved but nothing warrants a version bump — likely a
	# `commit --amend` after tagging, leaving ${LAST_TAG} pointing at a
	# stale commit. Offer to move the tag instead of computing a new one.
	echo "HEAD has moved past ${LAST_TAG} but nothing since it would bump"
	echo "the version (commit --amend?)."
	confirm_and_push_tag "${LAST_TAG}" "Re-tag ${LAST_TAG} to HEAD and force-push it?" "Re-pushed" 1
	exit 0
fi

echo "Last tag: ${LAST_TAG}"
echo "Next tag: ${NEXT}"
echo

confirm_and_push_tag "${NEXT}" "Create and push tag ${NEXT}?" "Pushed" 0
