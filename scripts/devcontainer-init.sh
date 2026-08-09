#!/usr/bin/env bash

THIS_SCRIPT=$(readlink -f "$0")
LOCAL_SCRIPT="${THIS_SCRIPT%.sh}.local.sh"
test -f "${LOCAL_SCRIPT}" && source "${LOCAL_SCRIPT}"

resolve_path() {
  local path=$1 result=()
  IFS='/' read -ra parts <<< "$path"
  for p in "${parts[@]}"; do
    case "$p" in
      ''|'.') ;;
      '..') [[ ${#result[@]} -gt 0 ]] && unset 'result[-1]' ;;
      *) result+=("$p") ;;
    esac
  done
  printf '/%s\n' "$(IFS='/'; echo "${result[*]}")"
}

SCRIPT_DIR=$(dirname "${THIS_SCRIPT}")
DCL=$(resolve_path "${SCRIPT_DIR}/../.devcontainer/docker-compose.local.yml")
DCLT=${DCL%.yml}.yml.example
test -f "${DCL}" || cp "${DCLT}" "${DCL}"