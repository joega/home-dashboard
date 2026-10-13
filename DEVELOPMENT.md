# Development path

This is a retrospective record of the initial development, written on October 13,
2026. The initial implementation was developed locally before Git commits began;
the commits are real snapshots from that point onward, not a reconstruction of
intermediate versions that were never saved in Git.

## Hardware and dashboard are separate

The original inspiration was [Wes Bos’s photo-frame-dashboard](https://github.com/wesbos/photo-frame-dashboard/).
That project handles frame-specific access, boot patches and the Android kiosk.
Home HQ is a separate application: Go, embedded HTML/HTMX assets and SQLite.
It can be developed with a normal browser and Go server. Container packaging is
left until deployment; moving a Docker image will not require a registry.

The PF1007L work used a microSD-triggered FEL entry, RAM-loaded U-Boot and a verified
original eMMC backup before enabling USB ADB. The stock Android kernel remains;
replacing the launcher is not a clean operating-system replacement. Vendor apps
and unnecessary services were disabled, unrestricted su removed, boot artwork
made black, and frame network access limited to the configured dashboard endpoint.
USB ADB is retained for maintenance. Per-device backups, identifiers and signing
keys belong outside public source control. Model-specific findings stay with the
hardware project, rather than becoming universal installation claims here.

## Initial application

1. Today, Calendar, Chores and Shopping established the local household model.
2. The original narrow icon sidebar became a left-aligned top menu with small
   outer gaps, following actual frame testing and the direction in
   [Kitchen Frame](https://github.com/huntsyea/kitchen-frame).
3. Compact headings and Today summary tiles replaced oversized headers and full
   task lists so useful information fits on the frame.
4. People settings introduced stable assignments and chore completion history.
   Recurring events and chores added weekday/custom schedules; Shopping gained
   configurable stores. Calendar locations are plain text for now.
5. Weather adapted the existing A Weather App provider validation, ZIP resolution,
   caching and solar calculations. Static scenes came first; animation and event
   forecast badges remain deferred pending hardware and location work.
6. Automatic light/dark appearance followed home sunrise/sunset. Separate native
   night backlight scheduling and optional touch-inactivity dimming were added.
   Sensor tests found an ambient-light response, not a verified presence signal;
   motion wake and full screen-off were deferred.
7. Cookbook adds manually entered paper recipes and favorites as the primary
   collection, Schema.org Recipe import/export, secondary TheMealDB discovery,
   cooking checklists, editable Shopping transfers and a simple dinner picker.
   Its saved-photo and ingredient workflows adapt Kitchen Frame under MIT.

The first source snapshot combines these initial iterations. Tests and design
notes explain the resulting behavior and important constraints. Future features
and fixes should be committed as coherent, tested changes when they are ready.

## Remaining direction

Sports will reuse provider behavior from Sportray through Go adapters. Recipe
wheel animation, serving scaling, richer calendar locations/integrations, weather
animation and final container packaging are future work. The small screen and
restricted frame network remain design constraints: provider requests happen on
the server, while assets and data reach the kiosk from one dashboard origin.

## License and data

Application source is MIT licensed; attribution and retained upstream licenses
are in THIRD_PARTY_NOTICES.md and web/static/vendor. Provider content has its own
terms and is not relicensed by the application license. Household databases,
uploaded photos, caches, credentials and generated binaries are ignored by Git.
