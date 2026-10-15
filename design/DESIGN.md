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


## Weather forecast

Weather is a top-level section, also reached by tapping Today’s atmospheric weather
strip. Keep the same compact page heading and translucent shell. Reuse the current
conditions strip above wind and sunrise/sunset, followed by a horizontally scrolling
hourly outlook and seven daily cards. Selecting a day changes its hourly outlook;
Next 24 hours returns to the rolling forecast. The normal 1024×600 view fits all
seven cards; smaller heights can scroll vertically. Phones scroll the daily cards
horizontally and show condition labels. Forecast scrollers retain their positions
through background swaps. Use static SVG weather icons and CSS skies; animations
remain deferred. Empty, disabled, stale and limited-coverage states stay explicit.


## Household notes

Notes uses a responsive tiled board with muted butter, sage, peach, sky and lilac
paper, with matching darker colors at night. Keep the existing weather gap and
compact page header. Three columns at 1024×600 fit six typical messages; narrower
tablets use two, phones use one, and wide desktops use four. Longer boards scroll
and paginate at 30 notes. Tiles preview the title and message, show the recipient
(or Everyone), author/avatar and creation time, and offer a 44 px pin control.
Pinned notes sort first, then newest. Tap a tile for the full message in a larger
reader, with Edit and Archive controls. Archived notes have a separate view and
can be restored with their original author and pin state.

The drawer editor uses plain text, a native recipient select, five labeled color
choices and an optional pin. New notes take the current person as author, or
Household when no person is selected; editing keeps that attribution. A recipient
is a friendly address, not a privacy boundary. Open editors pause background
refreshes. Conflicting edits retain the unsaved draft and offer an explicit way
to load the latest saved version. Dragging, corkboard textures, animation and
automatic expiry are outside this first version.


## Active-label navigation

The icon menu supersedes the earlier text tabs. Keep all nine sections in their
existing order with locally defined SVG icons; only the current section displays
its text label. Inactive icons retain explicit accessible names. Buttons measure
at least 48 px on the frame and 44 px on phones. Hover and keyboard focus show a
small tooltip outside the scrolling track without moving neighboring links;
Escape, a tap, navigation or scrolling dismisses it. Touch navigation is immediate
and does not wait for a hold. Preserve the current-person button, clock and
Settings on the right. Overflow arrows appear only when needed, and the selected
section stays visible on navigation and resize. The native scrollbar is hidden
for this menu; touch scrolling and the arrow controls remain available.


## Chore charts

Charts sits below the existing pending and completed lists inside Chores, in the
same vertical scroll area. Keep chores and their filters first; there is no
separate chart screen or navigation tab. Chart controls replace only the chart
section and retain the independent list filters.
Use a compact Day / Month / Year control, date input, person select and period
arrows. Cards stay in alphabetical order rather than ranking children against
one another. One retained completion is one star; selected-period badges unlock
at 1, 5, 10, 25, 50 and 100 stars, with a best run of consecutive household dates
with at least one recorded completion. These are derived progress markers, not
permanent awards or prizes. Unchecking a completion retracts its star.

At 1024×600, four helpers plus Household fit on one card row above a static bar
chart, reached by scrolling below the chores. Day shows the selected day's totals with seven days of context; Month
shows daily bars and Year shows monthly bars. Upcoming dates are muted dashes,
not failures. Phones use two card columns and a horizontally scrolling plot with
an explicit swipe hint. Colors follow the existing light/dark palette. Keep the
one-star rule visible and put the detailed credit rules in a disclosure. More
people and an expanded disclosure may scroll vertically. Open date inputs,
changed filters and expanded help pause background refreshes. Plot positions survive
normal background swaps without carrying over to another period or person.

Use chore_history as the credit ledger: never count shopping, assignments,
current done flags or the append-only activity log as additional achievements.
Use its saved household date, stable credited person ID and current display
name/photo. Preserve archived helpers with period history and keep shared credit
separate. Do not infer credit from old completions that lack history, or calculate
percentages without a historical record of what was actually due.
