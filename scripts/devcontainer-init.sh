#!/usr/bin/env bash

THIS_SCRIPT=$(readlink -f "$0")
LOCAL_SCRIPT="${THIS_SCRIPT%.sh}.local.sh"

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
  local joined=""
  for p in "${result[@]}"; do
    joined+="/$p"
  done
  printf '%s\n' "${joined:-/}"
}
export -f resolve_path

SCRIPT_DIR=$(dirname "${THIS_SCRIPT}")
DCL=$(resolve_path "${SCRIPT_DIR}/../.devcontainer/docker-compose.local.yml")
DCLT=${DCL%.yml}.yml.example
test -f "${DCL}" || cp "${DCLT}" "${DCL}"

# docker-compose.local.yml bind-mounts ${HOME}/.local/bin into the
# container. If it doesn't exist on the host yet, Docker auto-creates it as
# root:root before the container starts, leaving it unwritable to vscode.
# Creating it here (on the host, before the container starts) avoids that.
mkdir -p "${HOME}/.local/bin"

DCU=$(resolve_path "${SCRIPT_DIR}/../.devcontainer/docker-compose.user.local.yml")
touch "${DCU}"

# Sourced last: the local script fills in DCU, so it needs resolve_path and
# SCRIPT_DIR to already exist.
test -f "${LOCAL_SCRIPT}" && source "${LOCAL_SCRIPT}"

echo "devcontainer-init: docker context=${DOCKER_CONTEXT}"
