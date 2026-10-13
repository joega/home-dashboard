#!/usr/bin/env bash
# Installed root-owned; keep deployment access outside the dashboard container.
set -euo pipefail
cd /var/lib/home-hq-updater/repo
if [ "$(stat -c %u .)" != 0 ] || [ "$(stat -c %u .git)" != 0 ]; then
  echo 'Expected a root-owned managed deployment checkout.' >&2
  exit 1
fi
exec /usr/bin/bash scripts/update-server.sh
