# Container environment

Home HQ is one Go process serving embedded HTML, HTMX, fonts and static weather
scenes, with pure-Go SQLite. No Node runtime, separate database service, systemd or
package updates inside the running container are needed. Home Assistant is a
separate future project and is not included in this Compose stack.

## Base image choice

| Runtime | Fit for this app |
| --- | --- |
| Debian 13 distroless static/nonroot (selected) | Minimal runtime, CA certificates, no shell/package manager; suitable for the statically linked Go executable. Use application logs and the native health check for diagnostics. |
| Alpine stable | Also compatible with CGO disabled. Includes a shell and BusyBox, which can simplify in-container inspection; add CA certificates. Not required for this app. |
| Debian 13 slim | Familiar shell/package tools and broad compatibility, but a larger runtime than needed. |

The build stage uses official Go 1.27.2 on Debian Trixie. The runtime uses Debian 13
distroless; both manifest-index digests are pinned in Dockerfile. Images are
immutable: container starts do not install updates, and pinned digests do not change
on rebuild. Review and update pins for security fixes deliberately; a stable image
still needs maintenance. Go dependencies are fixed by go.mod/go.sum. Keep ordinary
local Go development unchanged.

## Runtime layout

- One non-root process, UID/GID 65532, listening on container port 4173.
- Read-only container root; only `/data` (persistent) and `/tmp` (32 MiB tmpfs) writable.
- Named volume `home-hq_household-data` contains SQLite, its WAL sidecars and any
  explicitly created snapshots. Photos, recipe data and forecast caches live in
  SQLite; they are not baked into the image.
- No host networking, Docker socket, privileged mode or extra Linux capabilities.
- Initial resource ceilings: 1 CPU, 512 MiB memory and 64 processes/threads; Go's
  memory target is 384 MiB. Adjust after observing real household usage.
- Bounded Docker logs; native `-healthcheck` probes `/healthz` and opens no database
  of its own. An unhealthy status does not itself restart a container. The restart
  policy handles process exits and host restarts, while manual stops stay stopped.
- 30-second stop grace period for HTTP shutdown, weather cancellation and SQLite close.
- Server outbound HTTPS remains available for weather and recipe imports. The
  frame retains its existing single dashboard IP/port policy. Docker isolation
  does not make future Home Assistant integration credentials read-only.

Compose publishes to loopback by default. At final deployment set the host's LAN
address explicitly. The app currently has no login and must stay on a trusted
private network; do not expose port 4173 publicly. Protect the host too: Docker
port publishing is not a substitute for network access policy.

## Prepare and validate locally

```sh
umask 077
cp .env.example .env
# Edit .env as needed. Keep local testing bound to 127.0.0.1.
docker compose config --quiet
docker compose build
```

On hosts requiring sudo, run Docker commands with sudo. Do not add new Docker
socket permissions merely for these checks. The current Go preview already uses
port 4173: stop it before starting Compose on that port, or use another host port
for a disposable browser test. Keep 4173 for the final frame endpoint.

The repeatable smoke check builds and runs a disposable image/container on an
allocated loopback port, writes one synthetic shopping item, verifies restart
persistence and a consistent backup, checks confinement and the CA bundle, then
removes only its own test containers, volume and image tag. It never mounts `data/`.

```sh
sudo bash scripts/check-container.sh
```

Validated on October 13, 2026 on the Linux development laptop. The owner ran the
script with sudo and reported PASS for image build, native health, non-root/read-only
policy, SQLite writes/restart persistence, backup integrity and CA-bundle presence.
The first smoke attempt revealed that Docker reallocated its automatic host port
after restart; the script now refreshes that mapping and the complete rerun passed.
Go tests, vet, CGO-disabled static compilation, health-probe race checks and Compose
configuration validation also passed. Actual final-server deployment, household
migration and resource usage under a full household workload remain untested.
CA-bundle presence is checked; this smoke test does not assert live provider uptime.

## First start with an empty household

```sh
docker compose up -d --no-build
docker compose ps
docker compose logs --tail 50 dashboard
```

An empty named volume initializes a new household without demo entries. It does
not import the existing local preview database automatically. Follow the migration
procedure below to retain the current entries. Normal `docker compose down`
preserves the volume. Do not use `down --volumes` for household data.

## Move the image without a registry

Build on the laptop, export, copy the archive and load it on the final host:

```sh
docker compose build
docker save home-hq:local -o home-hq-image.tar
# Transfer home-hq-image.tar plus compose.yaml and .env.example to the server.
docker load -i home-hq-image.tar
cp .env.example .env
# Set DASHBOARD_BIND_IP=192.168.1.10 on that host.
```

Run `docker compose up -d --no-build --pull never` on the destination after data
migration. Source/Dockerfile are unnecessary there when using the loaded image.
The image must match the server architecture; for a different architecture build
with `docker buildx build --platform linux/arm64 --load -t home-hq:local .` (or the
appropriate platform). A source build on the final host is another option.

## Move the current household database

Create a consistent snapshot with the local executable; the target must not exist:

```sh
mkdir -p backups
bin/home-dashboard -db data/dashboard.sqlite -backup backups/household.sqlite
```

Transfer the snapshot to `backups/household.sqlite` beside Compose on the server.
For an **initial empty volume only**, create a stopped container, then import with
numeric archive ownership matching the non-root process. Commands below use GNU
tar on Linux:

```sh
docker compose create --no-build --pull never dashboard
container_id=$(docker compose ps --all -q dashboard)
# Before continuing, verify this is a new volume with no existing household DB.
# Docker cp reports an error if the database is absent; an existing file means stop.
docker cp "$container_id:/data/dashboard.sqlite" backups/destination-check.sqlite
# Only proceed if the previous command confirmed that the file does not exist.
tar --owner=65532 --group=65532 \
  --transform='s/^household.sqlite$/dashboard.sqlite/' \
  -cf - -C backups household.sqlite | docker cp --archive - "$container_id:/data"
docker compose up -d --no-build --pull never
```

This import must not overwrite an existing household. If the destination has
already been used, stop and back it up first; plan that replacement separately.
Do not copy just a running SQLite main file and omit its WAL. The consistent
snapshot includes committed WAL data. The original local preview is retained.

## Backups and updates

While the container runs, create a uniquely named consistent snapshot and copy it
out (choose a fresh filename each time):

```sh
mkdir -p backups
docker compose exec dashboard /usr/local/bin/home-dashboard \
  -backup /data/household-2026-10-13.sqlite
docker compose cp dashboard:/data/household-2026-10-13.sqlite backups/
```

Keep copies off the server as well. Check snapshot integrity and restore into a
separate test volume before depending on backups. Snapshots contain household
information and photos; handle them as private files.

For updates, back up first, review source/dependency/base-pin changes, rebuild and
rerun the disposable smoke check, then replace the production image deliberately.
Existing data stays in the named volume. There is no automatic image updater.
