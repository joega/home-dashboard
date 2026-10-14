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
- Default: bridge networking, no Docker socket, privileged mode or extra Linux capabilities. The optional Media discovery profile uses host networking on Linux.
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
configuration validation also passed. The owner subsequently confirmed LAN rendering on the PF1007L and successful
timer installation with an already-current server check. Household migration, a
subsequent scheduled code deployment and resource usage under a full household
workload have not been independently verified.
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

## Frequent development updates

Use a dedicated server checkout on `main`. Develop and test on your laptop, commit
and push to GitHub, then run this **as the checkout owner** on the server:

```sh
bash scripts/update-server.sh
```

The script requests sudo only for Docker if needed; do not run the whole script
with sudo. For the first update that introduces the script, bootstrap with
`git pull --ff-only` first. It requires Bash, Git, Docker Compose with `--wait`,
Python 3, tar and `flock` (util-linux on Linux).

It fetches `origin/main`, refuses dirty/ahead/diverged checkouts, and permits only
a fast-forward. The current container stays running during the build, whose
Dockerfile runs Go tests and vet. Immediately before replacement it creates a
consistent SQLite snapshot, copies it to `backups/update-…/household.sqlite`, and
checks integrity. The household named volume must remain the same. Compose
recreates the service and waits up to 90 seconds for health. There is brief downtime
during replacement; do not make household edits in that window.

The image carries its Git revision. Later runs skip rebuilding/restarting when
that revision is already running and healthy. `--check` fetches and reports source
changes without deploying; `--force` rebuilds/redeploys even at the same revision.
A failed build or backup leaves the previous container running. A failed startup
prints focused logs and the previous-image/config command; it does not blindly
restore a database that might have newer household edits or schema changes.

Each attempt keeps the old image under `home-hq:before-…`, the resolved previous
Compose configuration, and revision records in its private backup directory. The
resolved configurations may contain credentials: do not share or commit them.
Snapshots also remain in the Docker volume as `before-update-….sqlite`. Retain
several known-good updates and an off-server copy; periodically remove older
snapshots/recovery folders/image tags deliberately. No automatic pruning is done.
Do not use `docker compose down --volumes` or volume pruning for updates.

To recover, review the printed command or use the saved `rollback.compose.json`
with the same Compose project name. It pins the old image and configuration but
uses the **current** household database. If a future migration is incompatible,
stop and plan a restore of the saved SQLite snapshot separately; this can lose
changes made since the snapshot. Following an image rollback, run the update
script with `--force` when ready to redeploy the current source.

Page data refreshes automatically on the normal 30-second cycle. Open clients also
check the server's application version once a minute. An update reloads the current
URL after at least 60 seconds without touch/keyboard/wheel activity. Open editors,
settings/import forms, unsaved form changes, Shopping ingredient review, cooking
mode, focused text/select inputs and active HTMX requests defer the reload. Browsing the
Cookbook normally can update; cooking waits until you leave that mode. Pending
updates are rechecked against the server before reloading; deployment downtime,
failed/malformed responses and rollbacks do not force a reload of the current
screen. Hidden browser tabs resume checks with a new idle period when shown.

The version fingerprints all embedded templates/assets and the production Git
revision supplied by this script, so backend-only production updates also count.
Reloading keeps the current URL and local cooking checklist storage. No Android
permission, kiosk update, firewall change or frame reflashing is involved.

**One-time bootstrap:** clients running the older code need one full reload after
the first deployment that adds this detector (frame **⋮ → Reload dashboard**).
Thereafter server updates apply automatically when the client is idle. A frame
power cycle also loads the detector. Normal Cookbook/editor data refresh remains
paused so it does not erase work.

Run the update command manually, or opt into the automatic timer below. Updating
source does not update pinned Go dependencies or base-image digests unless those
pins are changed in a commit. Run the disposable container smoke check for
packaging/dependency changes before deployment.

Script safety checks can be run without Docker access or a live server:

```sh
python3 scripts/test-update-server.py
```

These use disposable Git repositories and a simulated Docker CLI to check update
ordering, backup integrity, no-op updates and failure handling. They do not replace
an actual deployment health check.

The idle-update policy also has dependency-free JavaScript tests:

```sh
node scripts/test-idle-update.cjs
```

Node is needed only to run these development tests, not to build or run Home HQ.

## Optional automatic updates on Linux / Omarchy

The server can check public GitHub `origin/main` automatically. A host systemd
service runs the existing guarded update script, with a timer defaulting to about
**30 minutes after each completed run**. It also checks five minutes after boot;
on an already-booted machine the first check may run immediately after installing.
A failed run is logged and the timer checks again later. Successful no-op checks do
not rebuild or recreate the container. Only Home HQ's dashboard container is
replaced for an update; the Docker daemon and other containers are not restarted.

Install **on the deployment server**, from its checkout after pushing these files
to GitHub and pulling them there:

```sh
git pull --ff-only
sudo bash scripts/install-auto-update.sh --interval 30
```

Use `--interval 15` or `--interval 60` instead if preferred; rerun the installer to
change it. The installer checks that the existing `home-hq` dashboard is healthy,
clones the same public HTTPS origin on `main` into a private root-owned managed
checkout at `/var/lib/home-hq-updater/repo`, copies the server's `.env` on first
installation, validates and installs the unit files, and enables the timer. It
requires the existing Docker daemon plus Bash, Git, Python 3, tar, util-linux and
systemd. Those are normally available on the Omarchy server. SSH/private-repository
credentials are not configured by this installer. The timer is prepared in this
repository; it is **not** enabled until you run the installer on the server.

The managed checkout uses the same Compose project name and household volume.
Your original checkout is preserved and receives no root-owned Git files. After
installation, make production environment changes in the managed `.env` using
`sudoedit /var/lib/home-hq-updater/repo/.env`. Later installer runs preserve it.
The dashboard still has a read-only root filesystem, runs as non-root and has no
Docker socket, updater endpoint or host deployment permissions. The host service
has Docker deployment access; it trusts code pushed to the configured `main`.
No Docker group membership, socket permission changes or passwordless sudo rule
is added. The installed wrapper/units change only when the installer is rerun;
application code and the update script advance with GitHub.

Trigger a check/update immediately, view the next check and inspect results:

```sh
sudo systemctl start home-hq-update.service
systemctl list-timers home-hq-update.timer
sudo journalctl -u home-hq-update.service -n 60 --no-pager
```

An inactive oneshot service after a successful run is normal; look for the final
PASS/no-change line or the error in its journal. Systemd serializes these manual
and scheduled runs, and the script also locks its checkout. Once installed, use
`systemctl start` for manual updates; the original checkout's update command
directs you to it rather than racing a different checkout. Recovery files live
under `/var/lib/home-hq-updater/repo/backups/` and require sudo to read.

Pause before deliberate rollback or maintenance:

```sh
sudo systemctl disable --now home-hq-update.timer
# Resume later:
sudo systemctl enable --now home-hq-update.timer
```

Pausing the timer does not stop Home HQ or interrupt an already-running update.
The updater does not automatically restore data after a failed startup; use the
logged recovery path and compatibility guidance above. Logs/failures can be
inspected over SSH. This setup does not create an external notification channel.
To remove scheduling, disable the timer; retain the managed checkout/backups until
you no longer need recovery. Neither disabling the timer nor the installer removes
household data.

To review generated units without touching services:

```sh
bash scripts/install-auto-update.sh --prepare /tmp/home-hq-units --interval 30
systemd-analyze verify /tmp/home-hq-units/home-hq-update.service /tmp/home-hq-units/home-hq-update.timer
python3 scripts/test-auto-update.py
```

## Media discovery in Docker

The default bridge can reach a TV by private IPv4 address, but typically cannot
receive the LAN's mDNS/SSDP advertisements. Start with Settings → Media → Add by
address; this requires no container changes. Use a router DHCP reservation for
address-only entries. Even a saved stable identity cannot find a changed IP if
multicast discovery is blocked.

For automatic LAN discovery on a **Linux** server, an optional Compose overlay is
included. It shares the host network namespace, which broadens the container's
network access (including host loopback). It retains the non-root user, read-only
root, capability drop, resource limits and existing household volume; no Docker
socket is mounted. Bind the HTTP server to the intended LAN IP, not every interface.
This profile has not been live-tested on Mighty; the local Go discovery and the
resolved Compose policy were checked on the laptop.

After this version is deployed, set these in the active checkout's `.env`:

```dotenv
DASHBOARD_BIND_IP=192.168.1.10
DASHBOARD_PORT=4173
COMPOSE_FILE=compose.yaml:compose.media-host.yaml
```

Then explicitly recreate just the dashboard with the existing image:

```sh
sudo docker compose config --quiet
sudo docker compose up -d --no-build --pull never dashboard
sudo docker compose exec dashboard /usr/local/bin/home-dashboard -healthcheck
```

For the installed automatic updater, the active checkout/configuration is
`/var/lib/home-hq-updater/repo/.env`. Edit that managed file after the overlay has
arrived there; changing the original user's checkout does not change the updater.
Pause the timer while making this deployment configuration change, apply Compose
from the managed checkout, verify health and Media discovery, then re-enable it.
Future updates retain `COMPOSE_FILE` through `.env`; the same household named volume
is preserved. Do not run `down -v`. Remove `COMPOSE_FILE` and recreate the dashboard
to return to the default bridge. No host-network profile is enabled automatically.

Pairing credentials are private SQLite data and are included in database backups.
Keep host directories, volumes and backup files private. Never publish the
container port to the internet. The frame's single dashboard-IP/port firewall does
not need to change for Media: all TV protocol traffic originates from this server.

## Spotify login on the server

No new container ports, environment secrets, Docker permissions or network profile
are needed for Spotify. The Go server makes ordinary outbound HTTPS requests using
the existing CA bundle. Its account tokens persist in the household SQLite volume,
so normal automatic updates preserve the connection.

Spotify does not accept a plain-HTTP LAN callback. Keep the deployed dashboard at
`192.168.1.10:4173`; use a temporary laptop loopback tunnel only for account login.
After deploying the Spotify connector, register this URI in your Spotify developer
app and use it as the callback in the server's Settings → Media:

```text
http://127.0.0.1:4184/media/spotify/callback
```

On the laptop, keep this SSH command running during login:

```sh
ssh -N -L 127.0.0.1:4184:192.168.1.10:4173 joeg@192.168.1.10
```

Open `http://127.0.0.1:4184/settings?section=media` in your browser, save your public
Client ID and that callback, select Connect Spotify and approve the permissions in
that same browser. The callback passes through SSH to Mighty: tokens are stored in
Mighty's database, not on the laptop. After Media shows the account and current
playback, end SSH with Ctrl-C. No router forwarding, public hostname or permanently
running tunnel is required. Future token refresh happens directly on Mighty; an
explicit reconnect would use the tunnel again. These instructions assume SSH access
is already configured; do not broaden SSH/Docker privileges merely for login.

For a local Go preview at port 4182, use its separate registered loopback callback
instead. The local and server databases have independent connections. Do not copy
a preview database over the deployed household database just to move OAuth tokens.
Register both URIs if you want to test on the laptop and then authorize production.
The frame remains restricted to the existing dashboard IP/port.
