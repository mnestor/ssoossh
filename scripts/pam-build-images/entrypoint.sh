#!/bin/bash

ARCH=${1}
ACTION=${2}
SNAPSHOT=${3}

cd /workspace
GOARCH=amd64 goreleaser ${ACTION} --config .goreleaser-pam-${ARCH}.yml --clean --snapshot=${SNAPSHOT} --verbose