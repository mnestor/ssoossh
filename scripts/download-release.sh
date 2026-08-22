#!/bin/bash
set -euo pipefail

# Get latest release
echo "Fetching latest release for ${REPO}..."
LATEST_RELEASE=$(GH_HOST=github.com gh release view \
  --repo "${REPO}" \
  --json tagName \
  --jq '.tagName' 2>/dev/null)

if [ -z "$LATEST_RELEASE" ]; then
  echo "Error: Could not fetch latest release"
  exit 1
fi

echo "Latest release: ${LATEST_RELEASE}"

# Download the release
echo "Downloading release assets matching: ${PATTERN}"
GH_HOST=github.com gh release download "${LATEST_RELEASE}" \
  --repo "${REPO}" \
  --pattern "${PATTERN}" \
  --clobber

echo "Download complete!"

ARCHIVE=$(find . -maxdepth 1 -name "${PATTERN}" -type f -print -quit)

if [ -z "$ARCHIVE" ]; then
  echo "Error: No archive found matching ${PATTERN}"
  exit 1
fi

echo "Extracting: ${EXTRACT} from ${ARCHIVE}"
tar -xzf "${ARCHIVE}" --wildcards "${EXTRACT}"
rm -rf "${ARCHIVE}"