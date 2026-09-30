#!/bin/bash
set -e

if [ -d ./bin/alertmanager ]; then
  find ./bin/alertmanager -mindepth 1 -delete
  rmdir ./bin/alertmanager
fi

for dir in ./bin/alertmanager-[0-9]* ./bin/alertmanager-install.*; do
  if [ -d "$dir" ]; then
    find "$dir" -mindepth 1 -delete
    rmdir "$dir"
  fi
done

for dir in ./data/alertmanager ./data/alertmanager-test; do
  if [ -d "$dir" ]; then
    find "$dir" -mindepth 1 -delete
    rmdir "$dir" 2>/dev/null || true
  fi
done
