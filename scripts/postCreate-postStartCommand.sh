#!/usr/bin/env bash

sudo chown -R vscode:vscode /node_modules
find /workspace -name node_modules -type d -prune -print | while read -r nm; do
  dir=$(dirname "$nm")
  target="/node_modules/$(echo "$dir" | sed 's|/|_|g')"
  mv "$nm" "$target"
  ln -sfn "$target" "$nm"
done

find /workspace -name package.json -not -path "*/node_modules/*" | while read -r pkg; do
  dir=$(dirname "$pkg")
  nm="$dir/node_modules"
  target="/node_modules/$(echo "$dir" | sed 's|/|_|g')"

  if [ -L "$nm" ] && [ ! -e "$nm" ]; then
    echo "Relinking target: $nm -> $target"
    rm "$nm"
    mkdir -p "$target"
    ln -s "$target" "$nm"
  elif [ ! -e "$nm" ]; then
    echo "Linking: $nm -> $target"
    # mkdir -p "$target"
    # ln -s "$target" "$nm"
  fi
done
