# Home HQ

A local household dashboard for the AiMOR wall frame, built with Go, HTML templates, HTMX and SQLite. A compact left-aligned top menu sits inside one translucent surface, with a 12 px outer gap on the frame (8 px on phones) to expose the weather sky. The frame hardware work stays in the separate photo-frame-dashboard project.

Inspired by [Wes Bos’s photo-frame-dashboard](https://github.com/wesbos/photo-frame-dashboard/),
with navigation and Cookbook workflow adaptations from
[Kitchen Frame](https://github.com/huntsyea/kitchen-frame). Provider requests run on
the server; the frame receives assets and data from one dashboard origin.

Frame access, boot patches and kiosk setup belong in the hardware project. This
web server does not replace a frame’s Android kernel or establish trusted firmware.
On the PF1007L setup used here, the stock kernel remains and network access is
restricted to the dashboard endpoint. Keep per-device backups, identifiers and
signing keys outside public source control. Household databases, uploaded photos,
caches, credentials and generated binaries are also ignored by this repository.

## Develop

Requires Go 1.26 or newer. No Node.js or Docker is needed.

```sh
go run ./cmd/home-dashboard -demo
```

Open http://127.0.0.1:4173. `-demo` seeds example data only when initializing a new database. Omit it for an empty household. The example-data footer can clear all demo events, chores, shopping and chore credit, including edits made while in demo mode (People and store settings are kept); use it before entering real data.

For a reusable binary:

```sh
go build -o bin/home-dashboard ./cmd/home-dashboard
bin/home-dashboard
```

SQLite defaults to `data/dashboard.sqlite`; entries, people, photos and completion history survive restarts. Existing free-text event/chore owners are migrated to People automatically; Anyone/Household remain unassigned choices. Static assets and time-zone data are embedded in the binary, so rebuild and restart after changing templates, CSS or JavaScript.

## Features

- Today: compact agenda rows and two-column summary tiles for pending chores by assignee (including Anyone) and shopping by store (including Any store). Counts include every pending item, not just the first few; chores count only when due on the household date, and picked-up shopping is excluded. Only groups with pending items occupy space. Tap a tile for that person's chores or store's shopping; total links open the full lists. Home weather shows temperature, feels-like, daily high/low and precipitation chance.
- Calendar: Day with person lanes, 3 days with date lanes, rolling Week agendas, Month with event dots and day drilldown. Morning, Afternoon and Evening jump through the time grid. Arrows and horizontal swipes move by the selected period. The selected view is remembered in a browser cookie.
- Calendar events: timed or all-day, optional end time, location and household member assignment. Repeat daily, weekdays, weekly, selected weekdays, monthly or yearly, with intervals of 1–99 and an optional inclusive end date. Calendar views expand a single stored series; edits and deletion apply to the entire series. Per-occurrence exceptions and edits are not yet supported. Overlap handling opens the full day agenda when necessary.
- Settings → People: persistent names, optional local JPG/PNG avatars, editing and reversible archiving. People populate chore and calendar assignment dropdowns; photos or initials also identify chore owners. Renaming keeps assignments linked. Archived people remain on existing assignments but are hidden from new ones.
- Chores: an Everyone / Anyone / person selector combines with Today, routine and All schedules views; the person choice persists while switching those views. Archived people with assignments remain filterable. One-time tasks or the same repeating schedules as events, with owner and routine filters. Today shows only due chores; All schedules lets you edit chores not due today. One-time chores remain available from their start date until completed. Recurring completion is scoped to each scheduled household date. Off-schedule chores cannot be marked complete.
- Chore completion history: records the household date, UTC timestamp, stable person ID and name/title snapshots from this version onward. Credit goes to the assigned person; the kiosk has no identity verification. Repeated completion requests do not add credit. Unchecking retracts that occurrence; renaming, archiving or deleting the chore preserves other past credit. Stable task keys prevent a reused SQLite row ID from inheriting another chore’s credit. Existing pre-history completions are not retroactively attributed or timestamped. The actual metrics/rewards screen is deferred. Schedule changes do not rewrite previously recorded credit.
- Shopping: quantities, picked-up state and configurable store assignments. The filter bar offers All items, Any store (unassigned) and configured stores. Add/edit stores in Settings → Stores; Stop & Shop, Target and Costco are initialized once as editable defaults. Renaming keeps assignments linked; archiving hides a store from new choices while retaining assigned items. New items default to the selected active store. Changing an item’s store follows its new store when viewing a filtered list. Clear picked up removes only items in the current filter (All items clears across stores).
- Settings → Weather: five-digit US ZIP, Fahrenheit/Celsius, weather on/off and background controls. ZIP 02760 (North Attleboro, MA) initializes once as the requested home default. Static sky scenes follow live conditions and server-calculated day/night. Appearance defaults to Automatic: light between home sunrise and sunset, dark at night, with Light/Dark overrides. The sun calculation works even if weather is off or unavailable. A small local appearance refresh updates only CSS once a minute, preserving open editors and unsaved settings. This does not change the LCD backlight. Neutral paper and clearly labeled sunny/cloudy/rain/storm/snow/fog/night previews are available; previewing a scene never alters weather readings.
- Settings → Display: persistent night hours and backlight level for the native kiosk. Default 21:00–07:00 at 8%; daytime follows Android manual/adaptive brightness, with an optional fixed level. Opt-in dimming after no touch activity (default five minutes / 5%) restores on touch and never increases the current level on this frame. The server supplies time and household time zone; the kiosk caches valid settings and continues the schedule during connection loss. After an offline app restart with cached night scheduling enabled, it uses night brightness until time synchronization. These controls affect the frame, not desktop browsers. Motion wake and full screen-off remain unavailable until a real presence signal and reliable wake path are verified.
- Weather foundation: one server-owned Open-Meteo request fetches current weather and seven days of hourly/daily forecasts, cached in SQLite. Successful forecasts refresh every 15 minutes; failures back off from one to 15 minutes and preserve usable cached data. Failed/late updates are labeled stale; current weather expires after two hours and falls back to an unavailable widget and neutral scene. Invalid data cannot replace the last good cache; ZIP changes isolate caches and discard late old-location writes. Page views do not fetch weather, and fresh cached forecasts survive server restarts. Inline weather settings pause periodic page refresh so edits are preserved.
- Cookbook: your own household collection comes first, with manual entry for paper recipes, optional JPG/PNG photos, notes and favorites featured ahead of other recipes. Search by title, ingredient or tag; filter by meal, tag, maximum time, favorites or archived recipes. URL and Schema.org Recipe JSON-LD imports open an editable review before saving. Explore provides secondary TheMealDB discovery; save and adapt discoveries into your own book. Cooking mode offers larger ingredients and step checklists with progress remembered in each browser; Finish records last-cooked time and clears that browser's checks. Select missing ingredients for Shopping, review names and quantities, and choose stores before transferring. Matching pending items are skipped. Archive/restore and portable JSON-LD export are included. Pick dinner chooses randomly from the current filters' saved, active Dinner recipes. The native kiosk pauses touch-inactivity dimming during cooking; the night brightness schedule still applies.
- Fonts, scripts, sky scenes and weather data are served to the frame from the dashboard origin. The server contacts Open-Meteo for weather and changed-ZIP resolution; the frame needs no external network access. Source adaptation notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Calendar-provider synchronization, weather on calendar events and animation are deferred. Container packaging is prepared for review; final deployment remains separate. Data is local to this server; this first version has no authentication and should stay on a trusted private network. The frame's restricted network policy does not protect other clients accessing this server.

Monthly/yearly rules use the last day of a shorter month when the start day does not exist (for example, January 31 → February 28). Selected-day intervals use Monday-based weeks anchored to the start date. Recurrences retain local wall-clock times; an occurrence at a nonexistent daylight-saving time is skipped. Start and end dates are inclusive. Existing daily chores are migrated without changing their completion history.

## Roadmap

### Weather

The static background, Today widget, home ZIP/units settings and persistent forecast cache are implemented. Current defaults use ZIP 02760. Calendar forecast badges remain deferred until events support explicit weather locations; their foundation can use the cached hourly/daily forecast without separate requests per event. Animation remains deferred until responsiveness and rendering cost have been measured on the physical frame; retain static fallback and an off control.

### Cookbook

The first complete Cookbook is implemented. The random dinner picker precedes a possible wheel animation. Serving scaling, automatic ingredient merging, OCR of handwritten recipes and additional formats such as Cooklang remain future work. Saved recipes are local and survive provider outages; browser checklists are device-specific and reset when recipe content changes. TheMealDB recipes do not always supply timings or servings, so those fields are labeled missing rather than inferred from the title.

### Home status / Echo information (planned; waiting for Home Assistant)

Deferred until Home Assistant is installed and running on the eventual dashboard
server. The owner revised the initial Music-controls idea: device views on the
frame must be **status-only**, including thermostat, lock, cameras and Echo devices.
Children using the frame must not be able to change device settings. Existing
Calendar, Chores, Shopping and Cookbook edits remain household dashboard features;
this restriction applies to connected home-device commands.

First verify compatible integrations and their actual permissions. The standard
[Nest integration](https://www.home-assistant.io/integrations/nest/#sensor) requires
thermostat access-and-control permission even for temperature/humidity sensors;
a monitoring screen does not make its upstream credentials read-only. Do not pair
the thermostat or lock merely to obtain data until this tradeoff has been reviewed.
Home Assistant user permissions and Docker isolation are useful layers, not a
universal read-only mode for integrations, background tasks or a compromised server.

The Go backend will expose only bounded, allowlisted device-state snapshots and
approved image views. No general Home Assistant API proxy, service calls, device
setting forms, free-form Alexa commands or frame-accessible administrator session.
Credentials stay on the server, outside Git and browser storage. Use and verify a
read-only Home Assistant identity where supported; non-admin status alone is not
read-only. Before connecting real devices, test denied control requests against
virtual test entities and verify that modified frame requests cannot reach a device
command. Isolate the Home Assistant service from the frame and public internet;
keep the frame limited to its existing dashboard IP/port.

A truly read-only upstream connection must be supported by the device/provider.
Otherwise leave that device out, or separately review a credential-isolated gateway
that publishes only snapshots. Do not promise that a container or ordinary network
firewall can distinguish reads from writes inside an encrypted cloud API connection.
No device automations, heating control or door unlocking are planned.

[Kitchen Frame's Music view](https://github.com/huntsyea/kitchen-frame/blob/main/dashboard/src/views/Music.svelte)
remains a visual reference for now-playing artwork and room selection; playback,
volume changes and Music Assistant streaming are deferred. Cooking timer
announcements on the kitchen Echo would be a separate, explicitly approved output
path with a fixed speaker, bounded messages and quiet hours. They are not part of
strict status-only mode and must not enable general Alexa commands.

A PF1007L check on October 13, 2026 found an `audiocodec` ALSA device and Android
speaker routes. A temporary native tone test completed 0.6-second and one-second
PCM playback at media levels 1/8 and 2/8, with further amplitude attenuation. The
first was inaudible; the owner possibly heard the second very faintly. AudioTrack
consumed all samples, but usable physical speaker loudness is not yet conclusively
verified. Media volume was restored to its previous value and the temporary payload
removed; no kiosk change was needed. Timer sounds remain conditional on a clearer
speaker check. Local frame sound is separate from controlling music on an Echo.

### Sports (planned)

Favorite-team settings and a Sports view for upcoming, live and recent games, with scores and start times. Adapt the provider normalization, team identities, freshness rules, bounded caches and adaptive polling from the separate `../sportray` project. Its providers and models are JavaScript/QML, so reuse the behavior and fixtures in Go server adapters; do not require Omarchy or the desktop plugin to run. All provider data and any team logos must reach the frame through this dashboard's origin. Recheck provider behavior when implementing.

### Night display and presence

Automatic dark colors and configurable native night backlight dimming are implemented. Optional touch-inactivity dimming is also available. Full screen-off after a longer timeout and motion wake remain deferred: the PF1007L currently exposes ambient-light, acceleration and temperature sensors but no verified room-presence signal. Do not infer motion from light changes or frame acceleration. Investigate a genuine sensor interface or a future external presence integration before enabling screen-off. A dark webpage alone does not lower the LCD backlight.

## Recipe imports and discovery

[Schema.org Recipe](https://schema.org/Recipe) JSON-LD is the portable format used
for import/export. URL import reads this metadata without executing site scripts;
sites that block downloads or omit it require manual entry. Recipe text is retained
for review, so prices or preparation annotations from a source may need tidying.
Multiple recipes on a page produce a choice before editing. Export includes the
saved photo and household notes; imports accept up to 2 MB of JSON-LD.

All external downloads happen on the server. URL imports reject credentials,
private/local addresses, unexpected ports, unsafe redirects and DNS results that
include non-public addresses. Responses, images, recipe fields and caches are
bounded. JPG/PNG photos are normalized to JPEG with a maximum 640 px side and
stored locally. The frame receives pictures and provider data only from the
dashboard origin. These are practical import safeguards, not a full security audit.

[TheMealDB](https://www.themealdb.com/api.php) discovery caches results and thumbnails
for 24 hours. Set `MEALDB_API_KEY` for your provider key; the default `1` is the
provider's developer/educational test key. Recheck provider terms before public
or commercial distribution; public app-store releases require a supporter key.
The application's MIT license does not relicense recipe-provider content.

## Configuration

| Flag | Environment variable | Default |
| --- | --- | --- |
| `-addr` | `DASHBOARD_ADDR` | `127.0.0.1:4173` |
| `-db` | `DASHBOARD_DB` | `data/dashboard.sqlite` |
| `-timezone` | `DASHBOARD_TIMEZONE` | `America/New_York` |

For the eventual internal server, bind an appropriate private interface, e.g. `-addr 192.168.1.10:4173`. The frame's configured dashboard URL is http://192.168.1.10:4173/. Keep development on loopback until deployment. The server supplies the household clock, avoiding reliance on the frame's inaccurate system time.

A pinned multi-stage [Dockerfile](Dockerfile), dashboard-only [Compose stack](compose.yaml), [.env.example](.env.example) and [deployment guide](DEPLOYMENT.md) are prepared. Production uses Debian 13 distroless with a static non-root Go executable, persistent SQLite volume and read-only root filesystem. Local development can continue with Go. The guide covers validation, preserving current data, and moving an image using `docker save`/`docker load` without a registry. For frequent server updates, run `bash scripts/update-server.sh` as the checkout owner: it fetches main, builds/tests, backs up SQLite, preserves the previous image/configuration and waits for container health. See the guide for recovery and the one-time auto-update bootstrap. Open clients check for a new application version each minute and reload after 60 seconds of inactivity, deferring while editing, reviewing ingredients or cooking. An optional host systemd timer checks GitHub main every 30 minutes; install it on the server with `sudo bash scripts/install-auto-update.sh --interval 30`. It keeps deployment access outside the web container and supports immediate checks over SSH.

## Backup and move

Create a consistent database snapshot, including any live WAL data:

```sh
mkdir -p backups
bin/home-dashboard -db data/dashboard.sqlite -backup backups/household.sqlite
```

The destination must not already exist. Preserve the original database before replacing it. Transfer this snapshot and the source project to the final host; build for its architecture and run with `-db` pointing to the snapshot. If copying the raw database instead, stop the server first and preserve its SQLite sidecars. Do not copy only the live main database file while the server is writing.

## Check

```sh
go test ./...
go vet ./...
```

Cookbook tests cover draft ownership, metadata import, public-network URL guards, image bounds, editing conflicts, provider caching, duplicate saving, recipe filters, filtered picking, export and atomic/idempotent Shopping transfers. Tests also cover persistent completion, database backups, calendar modes and overlap handling, validation, CSRF and cross-origin rejection, HTML escaping, demo initialization, legacy-owner migration, person rename/archive, upload validation and completion-credit persistence, recurrence intervals/bounds/month ends/daylight saving, scheduled chores and event-location escaping, store assignment/rename/archive, shopping filters and scoped clearing, weather provider units/arrays/timestamps, ZIP matching, refresh throttling/backoff, cache restart/location isolation, stale/expired scenes, scene-preview accuracy, automatic appearance and overrides, legacy theme settings, CSS-only appearance refresh, weather settings validation, display-policy validation/persistence, clock payload and CSRF protection, pending summary totals, stable group links and chore-assignee filtering. Browser validation is also required for layout and interactions. The physical frame has not yet been tested with this server over the LAN. On 2026-10-13, the Today weather widget, translucent scenes and touch navigation rendered on the PF1007L over USB with Wi-Fi off. A short static-calendar sample was nearly idle and reported about 73 MiB kiosk PSS; this is a baseline snapshot, not evidence about animation or long-running performance.

Cookbook was checked in a browser at 1280×800 and 390×844, with real URL import,
manual paper-recipe entry, favorites, editing, archive/restore, filtered picking,
cooking progress across reload and store-assigned Shopping transfers using an
isolated database. The PF1007L rendered library and cooking screens over USB;
cooking stayed at 55% beyond the one-minute idle threshold, and exiting resumed
5% idle dimming. The installed kiosk is Home HQ 1.5. Actual overnight cooking and
LAN deployment have not been tested. Import sites vary, and long ingredient/step
lists scroll inside their working panes.

MIT licensed. See [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY_NOTICES.md).

Vendored HTMX and fonts include their upstream licenses in `web/static/vendor/`. Design references are in `design/`; the top-navigation and inset-shell direction in `design/DESIGN.md` supersedes the earlier sidebar concepts.
