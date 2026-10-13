#!/usr/bin/env bash
# Build and verify disposable containers/data only; never mount the household DB.
set -euo pipefail
cd "$(dirname "$0")/.."
for tool in docker curl python3; do command -v "$tool" >/dev/null; done
docker info >/dev/null
check_id="home-hq-check-$(date +%s)-$$"
check_image="home-hq:$check_id"
check_volume="$check_id"
check_container="$check_id"
check_tmp=$(mktemp -d)
image_created=0
volume_created=0
container_created=0
cleanup() {
  check_result=$?
  if [ "$check_result" != 0 ] && [ "$container_created" = 1 ]; then
    echo 'Container check failed; final status/port mapping and recent logs:' >&2
    docker inspect -f 'state={{.State.Status}} bindings={{json .NetworkSettings.Ports}}' "$check_container" >&2 || true
    docker logs --tail 12 "$check_container" >&2 || true
  fi
  if [ "$container_created" = 1 ]; then docker rm -f "$check_container" >/dev/null 2>&1 || true; fi
  if [ "$volume_created" = 1 ]; then docker volume rm "$check_volume" >/dev/null 2>&1 || true; fi
  if [ "$image_created" = 1 ]; then docker image rm "$check_image" >/dev/null 2>&1 || true; fi
  rm -rf "$check_tmp"
}
trap cleanup EXIT
if docker volume inspect "$check_volume" >/dev/null 2>&1 || docker container inspect "$check_container" >/dev/null 2>&1 || docker image inspect "$check_image" >/dev/null 2>&1; then
  echo 'Refusing a test-name collision.' >&2; exit 1
fi
docker compose --env-file .env.example config --quiet
image_created=1
docker build -t "$check_image" .
volume_created=1
docker volume create "$check_volume" >/dev/null
container_created=1
docker run -d --name "$check_container" --user 65532:65532 --read-only \
  --cap-drop ALL --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m,mode=1777 \
  --memory 512m --cpus 1 --pids-limit 64 \
  -v "$check_volume:/data" -p 127.0.0.1::4173 "$check_image" >/dev/null
wait_ready() {
  for ((attempt=0;attempt<30;attempt++)); do
    if docker exec "$check_container" /usr/local/bin/home-dashboard -healthcheck >/dev/null 2>&1; then return; fi
    sleep 1
  done
  docker logs "$check_container" >&2
  echo 'Container did not become healthy.' >&2; return 1
}
wait_ready
refresh_host_url() {
check_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "4173/tcp") 0).HostPort}}' "$check_container")
check_url="http://127.0.0.1:$check_port"
echo "Host endpoint: $check_url"
}
refresh_host_url
echo "Checking host reachability and writing disposable data..."
curl --noproxy '*' --retry 5 --retry-connrefused --retry-delay 1 --retry-max-time 10 -fsS -c "$check_tmp/cookies" "$check_url/shopping" -o "$check_tmp/page"
check_csrf=$(awk '$6=="hq_csrf" {print $7}' "$check_tmp/cookies")
test -n "$check_csrf"
curl --noproxy '*' --retry 5 --retry-connrefused --retry-delay 1 --retry-max-time 10 -fsS -b "$check_tmp/cookies" --data-urlencode "csrf=$check_csrf" \
  --data-urlencode 'title=Container persistence check' --data-urlencode 'quantity=1' \
  --data-urlencode 'return=/shopping' "$check_url/entries/shopping/0" -o /dev/null
echo "Restarting container and checking persistence..."
docker restart "$check_container" >/dev/null
wait_ready
# Automatically allocated host ports can change when Docker recreates networking.
refresh_host_url
curl --noproxy '*' --retry 5 --retry-connrefused --retry-delay 1 --retry-max-time 10 -fsS "$check_url/shopping" -o "$check_tmp/page"
python3 - "$check_tmp/page" <<'PY'
import pathlib,sys
assert 'Container persistence check' in pathlib.Path(sys.argv[1]).read_text(), 'data did not survive restart'
PY
docker inspect "$check_container" > "$check_tmp/inspect.json"
python3 - "$check_tmp/inspect.json" <<'PY'
import json,sys
c=json.load(open(sys.argv[1]))[0]
assert c['Config']['User']=='65532:65532'
assert c['HostConfig']['ReadonlyRootfs'] is True
assert 'ALL' in c['HostConfig']['CapDrop']
assert 'no-new-privileges:true' in c['HostConfig']['SecurityOpt']
PY
docker exec "$check_container" /usr/local/bin/home-dashboard -backup /data/verify.sqlite
docker cp "$check_container:/data/verify.sqlite" "$check_tmp/verify.sqlite"
docker cp "$check_container:/etc/ssl/certs/ca-certificates.crt" "$check_tmp/ca.crt"
python3 - "$check_tmp/verify.sqlite" "$check_tmp/ca.crt" <<'PY'
import pathlib,sqlite3,sys
c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro',uri=True)
assert c.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
assert c.execute("SELECT count(*) FROM entries WHERE title='Container persistence check'").fetchone()[0]==1
assert pathlib.Path(sys.argv[2]).stat().st_size>0
PY
echo 'PASS: image build, native health, non-root/read-only policy, SQLite writes/restart persistence, backup integrity and CA bundle.'
