#!/usr/bin/env bash
# Run as the checkout owner; only Docker commands use sudo when necessary.
set -euo pipefail

main() {
  cd "$(dirname "${BASH_SOURCE[0]}")/.."
  local mode=${1:-update}
  case "$mode" in
    update|--check|--force) ;;
    --help) echo 'Usage: bash scripts/update-server.sh [--check|--force]'; return ;;
    *) echo 'Unknown option. Use --help.' >&2; return 2 ;;
  esac
  if [ "$#" -gt 1 ]; then echo 'Use at most one option.' >&2; return 2; fi
  # Once scheduled deployment is installed, use its single managed checkout and
  # systemd job for manual triggers too; separate checkout locks would not overlap.
  if [ "$mode" != --check ] && [ -f /etc/systemd/system/home-hq-update.service ] &&
     [ "$PWD" != /var/lib/home-hq-updater/repo ]; then
    echo 'Automatic deployment is installed. Run: sudo systemctl start home-hq-update.service' >&2
    return 1
  fi
  for tool in git docker python3 flock tar; do command -v "$tool" >/dev/null; done
  umask 077
  mkdir -p .deployment
  exec 9>.deployment/update.lock
  flock -n 9 || { echo 'Another update is running.' >&2; return 1; }
  if [ -n "$(git status --porcelain)" ]; then
    echo 'Checkout has local changes; commit or resolve them before updating.' >&2; return 1
  fi
  if [ "$(git branch --show-current)" != main ]; then
    echo 'Use a dedicated server checkout on main.' >&2; return 1
  fi
  echo 'Checking origin/main...'
  git fetch origin main
  local target current
  target=$(git rev-parse FETCH_HEAD)
  current=$(git rev-parse HEAD)
  git merge-base --is-ancestor "$current" "$target" || {
    echo 'Local main is ahead or diverged; refusing to reset or merge it.' >&2; return 1
  }
  git log --oneline "$current..$target"
  if [ "$mode" = --check ]; then
    echo "Available revision: $target (checkout: $current). No deployment performed."
    return
  fi
  [ -f .env ] || { echo 'Configure .env before updating.' >&2; return 1; }
  local -a engine=(docker)
  if ! docker info >/dev/null 2>&1; then
    echo 'Docker requires elevated access; requesting sudo for Docker only.'
    engine=(sudo docker)
    "${engine[@]}" info >/dev/null
  fi
  local container old_image deployed project old_volume
  container=$("${engine[@]}" compose ps -q dashboard)
  if [ -z "$container" ] || [[ "$container" == *$'\n'* ]]; then
    echo 'Expected exactly one running dashboard; use the first-start guide otherwise.' >&2; return 1
  fi
  deployed=$("${engine[@]}" inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$container")
  if [ "$deployed" = "$target" ] && [ "$mode" != --force ]; then
    "${engine[@]}" exec "$container" /usr/local/bin/home-dashboard -healthcheck
    git merge --ff-only "$target"
    echo "Already running $target; no rebuild or restart needed."
    return
  fi
  "${engine[@]}" exec "$container" /usr/local/bin/home-dashboard -healthcheck
  old_image=$("${engine[@]}" inspect -f '{{.Image}}' "$container")
  project=$("${engine[@]}" inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$container")
  old_volume=$("${engine[@]}" inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$container")
  [ -n "$old_volume" ] || { echo 'Expected the household named volume at /data.' >&2; return 1; }
  local update_id recovery rollback_tag image new_volume
  update_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
  recovery="$PWD/backups/update-$update_id"
  rollback_tag="home-hq:before-$update_id"
  mkdir -p "$recovery"
  "${engine[@]}" tag "$old_image" "$rollback_tag"
  # Resolved configuration is private: it may contain provider credentials.
  "${engine[@]}" compose config --format json > "$recovery/rollback.compose.json"
  python3 - "$recovery/rollback.compose.json" "$rollback_tag" <<'CONFIG'
import json, pathlib, sys
p = pathlib.Path(sys.argv[1])
c = json.loads(p.read_text())
c['services']['dashboard']['image'] = sys.argv[2]
c['services']['dashboard'].pop('build', None)
p.write_text(json.dumps(c, indent=2) + '\n')
CONFIG
  printf '%s\n' "$current" > "$recovery/previous-checkout.txt"
  printf '%s\n' "$target" > "$recovery/target-revision.txt"
  git merge --ff-only "$target"
  "${engine[@]}" compose config --format json > "$recovery/target.compose.json"
  readarray -t config_values < <(python3 - "$recovery/target.compose.json" <<'CONFIG'
import json, sys
c = json.load(open(sys.argv[1]))
s = c['services']['dashboard']
v = next(v for v in s['volumes'] if v['target'] == '/data')
assert v['type'] == 'volume', 'Expected named household volume'
print(s['image'])
print(c['volumes'][v['source']]['name'])
CONFIG
  )
  image=${config_values[0]:-}
  new_volume=${config_values[1]:-}
  if [ -z "$image" ] || [ "$new_volume" != "$old_volume" ]; then
    echo 'Household volume configuration changed; stopping for manual review.' >&2; return 1
  fi
  echo 'Building and testing; the existing dashboard stays running...'
  "${engine[@]}" compose build --build-arg "HOME_HQ_REVISION=$target" dashboard
  if [ "$("${engine[@]}" image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" != "$target" ]; then
    echo 'Built image does not identify the requested source revision.' >&2; return 1
  fi
  echo 'Taking a consistent household snapshot before replacement...'
  "${engine[@]}" exec "$container" /usr/local/bin/home-dashboard -backup "/data/before-update-$update_id.sqlite"
  # Stream through the calling shell so sudo Docker cannot leave a root-owned copy.
  "${engine[@]}" cp "$container:/data/before-update-$update_id.sqlite" - | tar -xO > "$recovery/household.sqlite"
  python3 - "$recovery/household.sqlite" <<'BACKUP'
import pathlib, sqlite3, sys
p = pathlib.Path(sys.argv[1]).resolve()
with sqlite3.connect(p.as_uri() + '?mode=ro', uri=True) as db:
    assert db.execute('PRAGMA integrity_check').fetchall() == [('ok',)], 'Backup integrity failed'
BACKUP
  echo 'Replacing the dashboard and waiting for health...'
  if ! "${engine[@]}" compose up -d --no-build --pull never --wait --wait-timeout 90 dashboard; then
    echo 'Update did not become healthy. Recent logs:' >&2
    "${engine[@]}" compose logs --tail 25 dashboard >&2 || true
    echo "Recovery files: $recovery" >&2
    echo 'Previous image/config command (review database compatibility first):' >&2
    printf 'sudo docker compose -p %q -f %q up -d --no-build --pull never --wait\n' "$project" "$recovery/rollback.compose.json" >&2
    echo 'No automatic database restore was performed.' >&2
    return 1
  fi
  local running actual
  running=$("${engine[@]}" compose ps -q dashboard)
  actual=$("${engine[@]}" inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$running")
  [ "$actual" = "$target" ] || { echo 'Running image revision mismatch.' >&2; return 1; }
  echo "PASS: running $target. Household volume preserved."
  echo "Backup and previous deployment: $recovery"
  echo 'Open clients update automatically when idle; older clients need one full reload to install the detector.'
}
# Parse the whole function before Git changes this script during an update.
main "$@"
