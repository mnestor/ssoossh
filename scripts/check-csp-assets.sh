#!/usr/bin/env bash
#
# Fail if the built web UI references an asset the served Content-Security-
# Policy will refuse.
#
# The two drift apart silently and in opposite directions. The CSP lives in
# Go (server/middleware/csp_middleware.go); the assets are decided by Vite,
# which inlines anything under build.assetsInlineLimit as a data: URI. Bump a
# dependency, drop in a small icon, and the bundle grows a data: URI that no
# directive allows -- with no build error, no failing test, and no visible
# breakage beyond a console violation and a silently unstyled page.
#
# That is exactly how Fira Code's symbols2 subsets got inlined: two woff2
# files a few hundred bytes each, refused by `font-src 'self'` on every page
# load, falling back to the .woff sitting next to them. Nobody noticed until
# someone read a browser console.
#
# Scope: data: URIs, mapped to the directive that governs their media type.
# That is what the bundler actually emits -- it has no mechanism for pulling
# in a third-party host on its own -- and it is checkable without parsing
# minified JavaScript for string literals that merely look like URLs.
#
# Usage: check-csp-assets.sh [dist-dir] [csp-source-file]

set -euo pipefail

dist="${1:-server/frontend/dist}"
csp_source="${2:-server/middleware/csp_middleware.go}"

if [ ! -d "$dist" ] || [ -z "$(find "$dist" -type f -not -name '.gitignore' -print -quit)" ]; then
	echo "SKIP     no built web UI at $dist; run 'make frontend' first" >&2
	exit 0
fi

if [ ! -f "$csp_source" ]; then
	echo "ERROR    no CSP source at $csp_source" >&2
	exit 1
fi

# The policy as the Go source writes it: one "directive value; " string
# literal per line. Read from the source rather than from a copy kept here,
# so this cannot pass by agreeing with a stale duplicate of the header.
csp="$(grep -o '"[a-z-]*-src[^"]*"' "$csp_source" | tr -d '"' | tr '\n' '|')"

if [ -z "$csp" ]; then
	echo "ERROR    found no *-src directives in $csp_source" >&2
	exit 1
fi

# directive_allows_data <directive> -- is `data:` permitted there?
directive_allows_data() {
	printf '%s' "$csp" | tr '|' '\n' | grep -q "^$1 .*data:"
}

# The media types a data: URI can carry, and the directive that governs
# each. default-src is deliberately not consulted: a directive that is
# present but silent about data: does not fall back to it.
declare -A governs=(
	["font"]="font-src"
	["image"]="img-src"
)

status=0
for media in "${!governs[@]}"; do
	directive="${governs[$media]}"

	# -I so a data: URI inside a binary asset is not reported as text.
	hits="$(grep -rIlo "data:$media/" "$dist" 2>/dev/null | sort -u || true)"
	[ -z "$hits" ] && continue

	if directive_allows_data "$directive"; then
		continue
	fi

	status=1
	count="$(grep -rIo "data:$media/" "$dist" 2>/dev/null | wc -l | tr -d ' ')"
	echo "BLOCKED  the build emits $count inlined $media data: URI(s), but the served CSP says \"$directive\" without data:"
	printf '         %s\n' $hits
done

if [ "$status" -ne 0 ]; then
	cat >&2 <<'EOF'

The browser will refuse these and log a console violation on every page load.
Fix whichever side is wrong -- and they are not equally likely:

  * If the inlining was not intended (the usual case: an asset slipped under
    Vite's build.assetsInlineLimit), stop it being inlined in
    frontend/vite.config.ts so it is emitted as a same-origin file and the
    existing 'self' keeps working.
  * If it was intended, add data: to that directive in
    server/middleware/csp_middleware.go -- a deliberate widening of the
    policy, which is a decision to make in review rather than to arrive at
    by way of a size threshold.
EOF
	exit 1
fi

echo "csp assets: every inlined asset in $dist is permitted by the served policy"
