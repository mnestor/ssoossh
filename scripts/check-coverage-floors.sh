#!/usr/bin/env bash
#
# Fail if any package's statement coverage has dropped below its recorded
# floor.
#
# Why a floor per package rather than one number for the module: the module
# total moves for reasons that have nothing to do with the package someone
# just changed -- adding a well-tested package raises it and hides a
# regression elsewhere. client/cmd fell from 88.7% to 76.6% over a fortnight
# while the module total barely moved, which is the failure this exists to
# catch.
#
# The floors are a ratchet, not a target. Raising one is a normal part of
# adding tests; lowering one is a deliberate act that shows up in review as
# an edit to .coverage-floors, which is the point.
#
# Usage: scripts/check-coverage-floors.sh [coverage-profile]
# With no argument it runs the tests itself.

set -euo pipefail

FLOORS_FILE="${FLOORS_FILE:-.coverage-floors}"
PROFILE="${1:-}"

if [[ ! -f "$FLOORS_FILE" ]]; then
    echo "no floors file at $FLOORS_FILE" >&2
    exit 1
fi

if [[ -z "$PROFILE" ]]; then
    PROFILE="$(mktemp)"
    trap 'rm -f "$PROFILE"' EXIT
    # Both runs, for the same reason cover-ci does two: ./... cannot see
    # pam_ssoossh, whose every file is behind //go:build pam.
    CGO_ENABLED=1 go test -coverprofile="$PROFILE" ./... >/dev/null
    PAM_PROFILE="$(mktemp)"
    trap 'rm -f "$PROFILE" "$PAM_PROFILE"' EXIT
    CGO_ENABLED=1 go test -tags=pam -coverprofile="$PAM_PROFILE" ./pam_ssoossh/... >/dev/null
    # Concatenate, dropping the second profile's mode line.
    tail -n +2 "$PAM_PROFILE" >>"$PROFILE"
fi

# Per-package statement coverage, read straight from the raw profile.
#
# Not from `go tool cover -func`: that reports a percentage per function, and
# averaging those gives a function-weighted mean that does not match the
# number `go test -cover` prints. Two different figures for the same package
# is exactly the confusion a ratchet must not introduce, so this sums
# statements the way the toolchain does.
#
# Profile lines are "path/file.go:start.col,end.col numStatements count".
coverage_by_package() {
    awk '
        NR == 1 { next }                        # the "mode:" line
        {
            split($1, loc, ":")
            path = loc[1]
            sub(/\/[^\/]*$/, "", path)          # strip the filename
            sub(/^github\.com\/mnestor\/ssoossh\//, "", path)
            total[path] += $2
            if ($3 > 0) covered[path] += $2
        }
        END {
            for (p in total) {
                if (total[p] > 0) printf "%s %.1f\n", p, 100 * covered[p] / total[p]
            }
        }
    ' "$PROFILE"
}

declare -A actual
while read -r pkg pct; do
    actual["$pkg"]="$pct"
done < <(coverage_by_package)

failed=0
while read -r pkg floor; do
    [[ -z "$pkg" || "$pkg" == \#* ]] && continue

    got="${actual[$pkg]:-}"
    if [[ -z "$got" ]]; then
        echo "MISSING  $pkg has a floor of $floor% but produced no coverage" >&2
        echo "         (renamed or deleted? update $FLOORS_FILE)" >&2
        failed=1
        continue
    fi

    if awk -v a="$got" -v b="$floor" 'BEGIN { exit !(a + 0 < b + 0) }'; then
        echo "BELOW    $pkg is at $got%, floor is $floor%" >&2
        failed=1
    fi
done <"$FLOORS_FILE"

if [[ "$failed" -ne 0 ]]; then
    cat >&2 <<'MSG'

Coverage dropped below a recorded floor. Add tests for what you changed, or
-- if the drop is deliberate and justified -- lower the floor in
.coverage-floors, which puts that decision in front of a reviewer.
MSG
    exit 1
fi

echo "coverage floors: all packages at or above their recorded floor"
