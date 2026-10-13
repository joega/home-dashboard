# Dashboard design

The current references are `today-narrow-concept.png` and `calendar-day-concept.png`. Earlier images record the initial layouts; the user's narrow sidebar, neutral background and calendar-view requests supersede them.

Use a 72px icon rail at frame/desktop sizes, with Today first and no wordmark. Keep navigation labels accessible and available on hover. Use warm paper (#f6f4ee), a translucent neutral sidebar (ink at 4.5% opacity) with a subtle dividing rule and neutral selection (ink at 10% opacity), forest (#23493d) for calendar emphasis, terracotta actions (#bd6242), subtle rules (#c9cac2), locally served Lora headings and Inter controls. Favor visible rows and quiet separators over stacked cards. Primary controls have approximately 48px hit targets.

Today uses a wide agenda and a narrower chores/shopping column. Its clock comes from the server in the household timezone. Calendar uses a compact date header and Day / 3 days / Week / Month tabs; keep the frame's space for the schedule. Day lanes represent event owners, three-day lanes represent dates, Week uses compact rolling agendas, and Month uses dots that open a day. Timelines are 24-hour to preserve early/late events, with Morning/Afternoon/Evening jumps. All-day events sit above the scrolling timeline.

Intentional differences from mockups: live dates, clock and event data replace illustrated examples; person lanes are derived from real owner fields; typography uses distributable local fonts; time shortcuts use lighter controls; week/month layouts follow the user's Wes reference. Concept graphics are design references, not runtime assets. Phone layouts use horizontal top navigation, wrapping calendar controls and scrolling week columns.

Settings uses a People list with circular photos or initials, a name and assigned-chore count. Its editor follows the existing sheet pattern; assignment uses native select controls for WebView compatibility. Settings stays at the bottom of the desktop rail and is the fifth icon in the phone navigation.

Page headers use a compact 26px title (24px on phones), no generic subtitle, and 18px bottom spacing. Keep action buttons touch-friendly. Calendar retains the month/year context beneath its compact date navigation. List panels flex into the extra available height. This user direction supersedes the concepts' large page headings.

Event and chore editors share a Repeat selector, interval, selected-day checkboxes, start date and optional end date. Hide and disable irrelevant inputs. Editing affects a whole series and the sheet states that scope. Calendar locations appear in details and event rows where space permits. Today's chores contain only scheduled work; All schedules provides access to future/off-day chores with their completion controls disabled.

Shopping uses the same compact tab bar as Chores: All items, Any store and configured store names. Keep the selected store tab visible inside its horizontal scroller on narrow screens. Item forms use a native Store selector, defaulting to the selected active store. Settings → Stores shares the People list/editor style, with add, rename, archive and restore controls.

Weather uses a static CSS sky beneath content: sunny blue/gold, muted overcast, darker rain/storm, pale snow/fog and a night palette. Content uses a warm-paper surface at 78% opacity on the frame/desktop and 84% on phones, exposing more of the sky while retaining readable secondary text; text and controls remain opaque, with no backdrop blur, canvas or weather animation. Today has one compact weather strip beneath its header, retaining room for the agenda and household lists. Weather settings offer clearly labeled scene previews for frame testing; actual weather readings remain live. Backgrounds reflect current home weather even when browsing future calendar dates. Calendar-event weather remains deferred until event locations are more robust.

The top menu supersedes the 72 px icon rail. Use one rounded translucent shell with a uniform 12 px weather gap around the frame, 8 px on phones. Four labeled tabs (Today, Calendar, Chores, Shopping) sit in a left-aligned neutral track with a clear selected segment; clock and Settings are on the right. On phones the four tabs and Settings fit in one row and the clock is hidden. Today has an accessible hidden heading rather than duplicating its selected tab; other page titles/actions remain compact. This takes the navigation pattern from Kitchen Frame while retaining Home HQ's fonts, colors and household layout.

Appearance uses separate light/dark tokens for shell, text, controls, calendar event colors, fields and editors. Automatic follows the sun at the home coordinates using server time, independent of forecast freshness or background previews. The once-a-minute appearance response changes only CSS and preserves an open editor. The frame backlight is separate and unchanged.

Reference comparison: centered top navigation, four touch-sized labeled segments, neutral track, selected segment and Settings placement were inspected against Kitchen Frame's `home-light.png` screenshot. Intentional differences are the left-aligned menu to leave room for future modules, the requested outer weather gap, existing Lora/Inter type, compact clock, and our agenda/chores/shopping content rather than their clock-heavy card layout.

Today uses two-column count tiles for pending chores by assignee and shopping by store. Only nonzero groups appear; total links open all items, group links open the relevant filtered list. Agenda rows use 10 px vertical padding and 64–76 px minimum heights. Preserve scrolling for genuinely long agendas or many groups, but ordinary household data should fit the frame without a summary scrollbar. On phones the agenda and summaries stack vertically, while tiles remain two columns.


## Cookbook reference and implementation

`cookbook-concept.png` is the generated library reference; saved recipe content and
optional photos are real data, not the mock meals baked into that image.

| Reference | Implemented result | Decision |
| --- | --- | --- |
| Inset translucent shell, left top menu and right clock/settings | Same existing shell on the physical frame | Retained |
| Large serif Cookbook heading | Compact 26 px heading | Honors the household dashboard's requested compact headers |
| Search, meal, tags and Favorites | Same controls plus time and Archived filters, My cookbook/Explore tabs | Supports personal favorites first and secondary discovery |
| Three-column photo library | Three columns on the frame, two on phones, local bounded photos and text cards when absent | Keeps paper recipes first-class without invented images |
| Orange action buttons and star control | Pick dinner, Import URL, Add recipe, favorite/unfavorite controls | Retained; narrow phones scroll the top menu to keep the active section visible |

Recipe detail adapts Kitchen Frame's working-pane arrangement: source/photo,
ingredients already on hand, and steps. Cooking uses two larger working panes with
checklists; ingredients needed for Shopping are reviewed separately. Imports stay
editable and the interface labels missing provider data. Phone actions sit in one
row under the heading rather than consuming a tall vertical button stack.
