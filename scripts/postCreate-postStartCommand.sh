#!/usr/bin/env bash

if [ -f "/var/run/docker.sock" ]; then
  echo "setting up docker group"
  sudo groupadd -g 989 docker
  sudo usermod -a -G docker vscode
  newgrp docker
fi