#!/usr/bin/env bash
# Run once on the deployment host with sudo, not on the development laptop.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
source_dir=$PWD
interval=30
prepare_dir=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --interval) [ "$#" -ge 2 ] || { echo 'Missing interval.' >&2; exit 2; }; interval=$2; shift 2 ;;
    --prepare) [ "$#" -ge 2 ] || { echo 'Missing output directory.' >&2; exit 2; }; prepare_dir=$2; shift 2 ;;
    --help) echo 'Usage: sudo bash scripts/install-auto-update.sh [--interval MINUTES]'; echo 'Review only: bash scripts/install-auto-update.sh --prepare DIRECTORY [--interval MINUTES]'; exit 0 ;;
    *) echo 'Unknown option; use --help.' >&2; exit 2 ;;
  esac
done
if ! [[ "$interval" =~ ^[1-9][0-9]{0,3}$ ]] || [ "$interval" -gt 1440 ]; then
  echo 'Interval must be 1–1440 whole minutes (default 30).' >&2; exit 2
fi
render() {
  install -d -m 0700 "$1"
  install -m 0644 deploy/systemd/home-hq-update.service "$1/home-hq-update.service"
  sed "s/@INTERVAL@/$interval/g" deploy/systemd/home-hq-update.timer > "$1/home-hq-update.timer"
  install -m 0755 deploy/systemd/home-hq-auto-update.sh "$1/home-hq-auto-update"
}
if [ -n "$prepare_dir" ]; then
  render "$prepare_dir"
  echo "Prepared review files in $prepare_dir; no service installed or Docker/Git action performed."
  exit 0
fi
if [ "$EUID" != 0 ]; then echo 'Run installation on the server using sudo.' >&2; exit 1; fi
for tool in git docker python3 flock tar systemctl systemd-analyze install stat; do command -v "$tool" >/dev/null; done
[ -f .env ] || { echo 'Configure the server checkout .env first.' >&2; exit 1; }
[ -d /run/systemd/system ] || { echo 'A Linux host running systemd is required.' >&2; exit 1; }
docker info >/dev/null
project=$(docker compose config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')
[ "$project" = home-hq ] || { echo 'Expected the existing home-hq Compose project.' >&2; exit 1; }
container=$(docker compose ps -q dashboard)
if [ -z "$container" ] || [[ "$container" == *$'\n'* ]]; then
  echo 'Start exactly one healthy dashboard before installing automatic updates.' >&2; exit 1
fi
docker exec "$container" /usr/local/bin/home-dashboard -healthcheck
# Only this read uses a per-command safe.directory exception for the owner's checkout.
remote=$(git -c "safe.directory=$source_dir" remote get-url origin)
if ! [[ "$remote" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo 'Automatic installation currently supports public HTTPS GitHub origins only.' >&2; exit 1
fi
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL=/dev/null
state=/var/lib/home-hq-updater
if [ -L "$state" ] || { [ -e "$state" ] && [ "$(stat -c %u "$state")" != 0 ]; }; then
  echo 'Refusing a symlink or non-root-owned updater state directory.' >&2; exit 1
fi
install -d -m 0700 "$state"
if [ ! -e "$state/repo" ]; then
  git -c credential.helper= clone --branch main --single-branch "$remote" "$state/repo"
fi
if [ -L "$state/repo" ] || [ "$(stat -c %u "$state/repo")" != 0 ] || [ "$(stat -c %u "$state/repo/.git")" != 0 ]; then
  echo 'Expected a root-owned managed Git checkout.' >&2; exit 1
fi
if [ "$(git -C "$state/repo" remote get-url origin)" != "$remote" ]; then
  echo 'Existing managed origin differs; refusing to replace it.' >&2; exit 1
fi
[ -f "$state/repo/scripts/update-server.sh" ] || { echo 'Push the server update script to main before installation.' >&2; exit 1; }
install -d -m 0700 "$state/repo/.deployment" "$state/docker"
exec 9>"$state/repo/.deployment/update.lock"
flock -n 9 || { echo 'An update is already running; retry installation after it finishes.' >&2; exit 1; }
if [ ! -e "$state/repo/.env" ]; then
  install -m 0600 .env "$state/repo/.env"
else
  echo 'Keeping the existing managed .env; changes to the original checkout are not copied automatically.'
fi
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
render "$staging"
systemd-analyze verify "$staging/home-hq-update.service" "$staging/home-hq-update.timer"
install -d -m 0755 /usr/local/libexec
install -m 0755 "$staging/home-hq-auto-update" /usr/local/libexec/home-hq-auto-update
install -m 0644 "$staging/home-hq-update.service" /etc/systemd/system/home-hq-update.service
install -m 0644 "$staging/home-hq-update.timer" /etc/systemd/system/home-hq-update.timer
systemctl daemon-reload
flock -u 9
exec 9>&-
systemctl enable home-hq-update.timer
systemctl restart home-hq-update.timer
echo "PASS: automatic checks installed, about every $interval minutes after each run."
echo 'A boot-time check may run immediately after installation on an already-booted host.'
echo 'Run now: sudo systemctl start home-hq-update.service'
echo 'Logs: sudo journalctl -u home-hq-update.service -n 60 --no-pager'
echo 'Pause: sudo systemctl disable --now home-hq-update.timer'
echo 'Managed configuration: /var/lib/home-hq-updater/repo/.env'
