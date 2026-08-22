#!/usr/bin/env bash
set -euo pipefail

command -v "quill" >/dev/null 2>&1 || {
  exit 0
}

quill sign-and-notarize "$1"