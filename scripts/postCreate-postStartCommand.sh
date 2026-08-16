#!/usr/bin/env bash

if [ -f "/var/run/docker.sock" ]; then
  echo "setting up docker group"
  sudo groupadd -g 989 docker
  sudo usermod -a -G docker vscode
  newgrp docker
fi

# Self-heal ~/.local/bin if it was bind-mounted in as root:root by an
# earlier rebuild (devcontainer-init.sh now pre-creates it on the host, but
# this covers containers that already have a root-owned copy).
if [ -d "${HOME}/.local/bin" ]; then
  sudo chown -R vscode:vscode "${HOME}/.local/bin" 2>/dev/null || true
fi