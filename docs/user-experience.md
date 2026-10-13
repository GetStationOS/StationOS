# StationOS user experience standards

Status: adopted, 9 October 2026. Apply these rules to every new operator-facing screen.

## Product hierarchy

Stations are the top-level domain. `/producer` is the station overview, with one card per station and a clear Create station action. Opening a card leads to that station's workspace. Overview, Playlists and Settings retain the station identity and breadcrumb. New scheduling, presenters, output and reporting screens must join this station workspace instead of becoming unrelated global pages.

Main navigation contains Stations, Sources and DJs. DJs are shared fictional presenter profiles, managed through a concise list and dedicated create/edit forms. They own a name, writing/curation model, selected voice, conversation speed, prompt and voice-direction vibe. Conversation speed uses a labelled slider with a live multiplier display, from 0.25× to 4.00×, defaulting to 1.00×. It saves with the profile and affects newly generated speech. API-key fields are write-only: leave blank to retain, enter a replacement or explicitly remove. Reuse the standard footer and styled delete dialog. Creation/editing does not itself invoke AI or start broadcasting. Sources serve every station; their imported metadata is a shared implementation detail. Track browsing is opened while adding music to a playlist, retains the target station/playlist and offers a direct return to its Tracks tab. Never ask the operator to choose the same station again when the current workspace already supplies it.

## Status and information

Show a small number of useful, current facts on station cards. Playlist counts are measured; listener counts come from active broadcast subscriptions, including the app player. Browser probe and playback requests from one app listening session count once. Tiny bare-stream media probes finish after initial audio instead of remaining as duplicate listeners; separate app players and external playback connections remain independent. Dormant stations have a measured zero. Never use zero to represent unknown, invent listener numbers, show a playlist's first track as now playing, or label a station on air without an active output.

Station Overview separates current playback, upcoming programme, listeners and stream health. Playback is listener-driven; weekly schedules select the active programme at track boundaries. Show current/next only from the actual producer, with Dormant, Starting, Broadcasting, Retrying and Unavailable states. When those systems exist, render their actual state through their own service contracts. Use HTMX polling or server events for the status region only; do not repeatedly replace an editing form or reset its scroll/focus. Distinguish offline, loading, stale, failed and unknown states with text as well as colour.

## Navigation and HTMX

Keep the application shell stable. Use ordinary links/forms enhanced by HTMX to replace `#main`, retaining real URLs, page titles, browser back/forward and keyboard navigation. Full requests render a complete document; HTMX requests render a titled fragment. History restoration must recover the complete shell when necessary. Use `HX-Location` for successful writes, avoiding full-page redirects. Use `HX-Redirect` only when a full browser navigation is genuinely required.

New-screen navigation starts at the top. In-place saves and row actions preserve scroll. Every page has one h1 and clear station context. Active global navigation and station tabs use aria-current. Move focus to the updated main region after a screen swap without unexpected scrolling. Ordinary forms must remain usable without JavaScript.

## Selection and editing

For multiple choices, use labelled checkbox lists with search and selected counts. Select/Clear visible affects only the current filtered choices. Searching must never erase checked values. Preserve existing choices that are temporarily absent from the catalogue, label them clearly, and allow them to be unchecked.

Save related selections together, atomically. Genre/album rules combine as a union and automatically pick up new matching tracks; explicit track exclusions win. Show unsaved state and prevent accidental departure. Never prompt about unsaved changes during the successful save's own navigation. Keep manual selections and exclusions separate from rule edits. Playlists are unordered pools available to the station curator. Do not show sequence numbers or move controls; alphabetical display is for browsing and carries no playback priority.

Catalogue selection applies to the visible page; label its count accordingly. Enable Add selected tracks only when tracks and a target playlist are chosen. Use an indeterminate page checkbox when some rows are selected. Preserve filters, page size and target playlist across page changes and saves. Use one task per tab: playlist Selections, Tracks and Details. Creation uses an Add button and a dedicated form with Cancel, never a card disclosure. Station context supplies the owner automatically. Keep playlist count and duration alongside the title. Put Add tracks in the table toolbar alongside pagination, with no separate summary/action rows between the tabs and table. Show pagination once above the table, with range, previous/next, page jump and page size on one horizontal row. Keep that row scrollable on narrow screens.

Sources follow the same workspace pattern: a concise source list, dedicated Add source page, Overview with measured track/album counts and a refresh action, and Settings for connection fields. Saving or refreshing returns to that source.

## Feedback and recovery

Every successful write gives a short, concrete confirmation. Disable the submitted action during the request and show a loading indicator. Background status polling must stay quiet: scope its HTMX indicator and aria-busy state to the refreshed region, without showing the global Working indicator. Reserve global loading feedback for operator-initiated navigation and actions. On validation, server or connection errors, retain the form and operator input; show an accessible error message with a useful next step. Do not replace the whole screen with a raw error response. Bound request sizes and validate on the server even when the browser already validates.

Choose labels that describe the effect: Exclude for a track excluded from dynamic rules, Save selections for a combined rule update. Reserve red destructive styling for actions that destroy operator data. Playlist Details and station Settings expose delete actions. Use the shared styled dialog for delete confirmation and unsaved-change prompts: name the affected entity, describe the scope, focus the safe action, contain keyboard focus, support Escape and return focus on close. Keep normal confirmation-page fallbacks for delete links. Station deletion removes its playlists atomically; shared sources and music remain. Empty states explain what is missing and give one useful next action. Avoid inactive controls for future functionality.

## Form action standard

Place actions at the end of the form. Existing entities with deletion use one footer: red Delete on the left, then a right-aligned group with secondary Cancel followed by the primary Save action. Creation forms omit Delete and retain the same right-aligned Cancel/primary group. Use specific action labels (Save changes, Save selections, Create station, Create playlist, Add source), rather than Submit. Cancel returns to the relevant parent/overview; unsaved selection changes use the shared discard confirmation. Utility controls such as pagination and refresh keep their compact task-specific actions. In confirmation dialogs, Cancel precedes the red confirmation on the right and receives initial focus. Preserve DOM order when wrapping on small screens.

## Visual system and accessibility

Use tokens in `internal/web/static/app.css`: neutral canvas, white panels, teal primary actions, quiet borders, a consistent radius and spacing scale. Dark playback panels establish priority; use accent colours sparingly. Shared classes cover headings, cards, metrics, notices, buttons, forms, checkbox groups and pagination. Extend those components before adding page-specific variants.

Use system fonts, restrained typography, descriptive headings and plain language. Remove repeated station names, redundant headings, workspace slogans and explanations of planned features. Keep optional help collapsed; show unsaved text only when changes exist. Primary actions are visually prominent; secondary actions are quieter. Desktop navigation stays consistent; small screens use compact top navigation, stacked cards/forms and horizontally scrollable data tables. Long titles and metadata must wrap without breaking the layout.

Single-line fields and dropdowns use the shared 42px control height; pagination controls use 36px. Style closed dropdowns consistently while retaining native option menus and keyboard behaviour.

Every control needs a label, visible focus and keyboard access. Use fieldset/legend for choice groups, status/alert live regions for feedback, meaningful button names for row actions, a skip link and sufficient contrast. Status must not depend on colour alone. Respect reduced-motion preferences; never require hover to discover essential actions.

## Review checklist

- Start at the homepage and complete the feature in station context.
- Verify empty, populated, unknown, loading, error and success states.
- Verify bulk selection, hidden selected choices, clearing all and atomic failure.
- Verify direct URLs, refresh, browser back/forward and ordinary forms.
- Verify titles, active navigation, focus, keyboard labels and mobile layout.
- Check that unavailable data is labelled honestly and editing does not lose input.
- Test meaningful server behaviour; inspect real browser interactions for JavaScript/HTMX flows.

## Current implementation and limits

Station cards, per-station Overview/Playlists/Settings, playlist Selections/Tracks/Details, dedicated creation pages, single-row pagination, contextual track browsing, responsive components, HTMX navigation/forms/history, successful-save notices, error handling, searchable bulk checkboxes and unsaved-change protection are implemented. Station overview polls runtime state every three seconds. Homepage cards use a live event connection and patch only changed content in place; unchanged station nodes, artwork and controls are retained. Stream URLs are stable and editable in Settings; default playlist assignment is managed in each playlist’s Details tab. Playlist cards identify the station default with a labelled star badge in the top right. Set/remove actions refresh only the default control panel, preserving the other fields and page position. The changed badge and action confirm the new default state without an additional saved/removed message. A compact player in the persistent shell survives navigation and history, with Pause/Resume, volume and Stop controls. Pausing releases its listener connection and resumes at the live point. Never expose a seek bar for live radio. Playback starts only on explicit Listen; full reloads end playback.

## Weekly shows

Use a station Schedule tab with a weekly calendar, explicit local timezone and optional show list. Add show opens a dedicated form with a day checklist. Saving creates an independent weekly show for each selected day, atomically; a conflict on any day retains the choices and creates none. Selecting a card opens that day’s show details for individual editing. Use quarter-hour time controls, a shared DJ selector and a station-owned playlist selector. Reject overlaps with a useful message while retaining input. A show may cross midnight. Show deletion follows the common footer/dialog standard and leaves its DJ and playlist intact. Display measured current presenter/show state during playback; never label a planned presenter as actually on air. Starter DJs are editable configuration, seeded once with empty credentials.

## Producer cards

Homepage station cards show measured stream state and listener count, the actually running programme/DJ/time window/playlist, or the configured default playlist when no show is running. Dormant stations show empty current/next states, never a fabricated song or on-air DJ based only on the clock. Current and next rows include title, artist, album, duration and available artwork. During speech the current row shows DJ on air and the speaking presenter’s photo, using the same artwork selection as the persistent player; music restores the album cover. Show readiness, missing DJ credentials, handovers, overrunning tracks and the following show when known. Keep editorial/profile and playlist links contextual, with Listen and Open station actions. Live event updates preserve keyboard focus and stay quiet. Do not replace card nodes or reload unchanged artwork just to update a listener count. Explicitly target the main workspace for card links so a polling region cannot capture ordinary navigation.

Artwork uses authenticated same-origin station URLs for currently playing/queued items. Sources fetch it through their checked transport; browsers do not need direct access to LAN servers. Raster images are size/time bounded and privately cached; unavailable artwork retains a neutral placeholder. Keep cards compact and responsive without disclosures or invented metrics. Use one full-width station card per row. On desktop, arrange programme information, current track and next track horizontally; stack them on smaller screens. Each station determines its own height, so a long programme never leaves a gap above another station.


## Live overview updates

Use one authenticated server-sent event connection while the homepage is visible. Runtime changes and committed configuration changes notify the view; unchanged rendered content sends no update. Apply changed content in place and preserve unaffected cards, image nodes and keyboard controls. Close the connection on navigation or page exit and reconnect on return. Status observers never start a station or count as listeners. Keep connection heartbeats quiet; reconcile external configuration and clock changes without replacing unchanged content.


### Persistent player metadata

The shell player shows the actual current and next titles, performing artists and available album artwork, with DJ on air during speech. The current artwork switches to the speaking DJ’s photo for introductions, links and handovers, and returns to album artwork for music; the next-track cover stays unchanged. Square covers are 80px on station cards and 56px in the desktop player. Station-card covers reduce to 64px on small screens; the player hides artwork below 700px to give track text more room. Both reuse authenticated station artwork URLs and retain a neutral placeholder when artwork is missing or unreadable. A station-specific authenticated event connection updates only changed metadata and remains active across HTMX navigation. It does not start audio or count as a listener; switching stations replaces the connection and Stop closes it. Empty runtime states show Nothing playing and Not queued. The desktop player uses one compact row, with station identity, current/next tracks and icon controls. On phones it uses a concise station/status row, text-only current/next details and a volume/control row. Pause, Resume, Retry and Stop use action icons with accessible names and visible keyboard focus. Mobile buttons have 36px visible circles within 44px tap targets; titles are limited to two lines and artists to one line.


### Station Mixer

Mixer is a station workspace tab, alongside Overview, Playlists, Schedule and Settings. A dark broadcast console has separate Music and DJ channel strips, vertical faders with percentage scales, exact level fields, Mute and 100% reset controls, immediate automatic saving and inline saving/error feedback with Retry. Slider, percentage, mute and reset changes save without replacing the form or moving focus; requests are serialized and rapid changes coalesce to the latest levels. Channel strips share one reusable component; more supported audio channels can join the responsive console, with strips wrapping on narrower screens. Channel colours identify music and presenter controls; the scale shows configured gain, never invented live metering. Both levels range from 0% (mute) to 400%, defaulting to 100%. Changes commit automatically and affect every listener at the next track or announcement, including cached speech. The persistent app player's volume remains a personal listening control. Invalid input is retained with an accessible message; settings save together and survive restarts.


## DJ backgrounds and portraits

DJ edit forms include an optional Background profile and uploaded photo, with a preview of the saved portrait. The shared DJ list and measured on-air presenter show the portrait when configured. Existing portraits remain until replaced; admins upload JPEG/PNG files and use the X on the image to remove them. Profiles are fictional character material; prompts still control editorial choices and voice direction remains separate. The three original starter DJs receive distinct backgrounds and bundled AI-generated portraits once, preserving names, prompts, voices and encrypted keys. Renamed or deleted starter DJs are not re-created or re-characterised.

### DJ profile photos and settings

DJ creation and editing group Profile, Editorial style, Voice and API access in one form with a single Save footer. Profile includes a compact photo preview, labelled JPEG/PNG file chooser and an accessible remove X on the image. The file chooser uses the standard 42px height without a nested input border. The admin never enters a URL or server path. Selecting a file previews it locally; upload, replacement and removal commit with the other settings. Empty file selection preserves the saved photo. JPEG/PNG inputs are limited to 5 MB and 12 megapixels and normalized for display. Uploaded photos live with the profile and are included in database backups. Invalid uploads retain the existing photo and form; ordinary form errors explain when an upload must be selected again. Settings follow one bounded reading column on desktop and mobile, with quiet separators between groups. Background, prompt and vibe text areas have a consistent fixed 160px height with internal scrolling and no resize handle, keeping the form stable. Conversation speed uses the same 6px label-to-control gap as other fields. Custom model ID appears directly below its model selector only when Other model is selected.

## Station logos

Station creation and Settings reuse the DJ photo preview, JPEG/PNG chooser and accessible remove X. Local preview changes commit with the form. Empty upload preserves the saved logo; removal restores the bundled radio logo. Limits match DJ photos (5 MB, 12 megapixels). Station cards, Overview and the persistent player show station identity separately from album covers; player identity updates through its existing event connection.

Station names sit beside their logos on listing cards and the station Overview heading, including narrow screens. The floating player uses a dedicated logo column beside its station name/status so branding remains visible without overlapping track metadata. Supported browser and phone media controls receive the selected station logo, actual current title/performer and station name through the Media Session API; metadata follows station/track changes and clears on Stop. Native Play/Pause/Stop actions use the same listener lifecycle as the app controls.


## Track repeat interval

Station creation and Settings include Minimum time between plays (minutes), default 180, accepting whole minutes from 1 to 10080. It applies across the station's shows and playlists and survives restarts and listener disconnects. Help text explains that small pools use their least recently played alternative when necessary. Station cards and Overview show a quiet, measured Repeat interval relaxed notice only while that fallback track is being broadcast. Operators manage duplicate recordings through playlist exclusions; StationOS uses exact track identity.


## Public radio pages

The public homepage `/` lists only stations explicitly set to Listed. Each station has a direct page at `/radio/<stream-address>` with its logo, description, Play action, actual current/next tracks, running show/presenter, weekly schedule and a copyable MP3 stream URL. Show times use the station timezone, and overnight shows label their next-day end. Scheduled names are programme listings, never an assertion that a presenter is currently on air.

Station creation and Settings offer Listed, Unlisted and Private visibility. New and upgraded stations start Unlisted: existing direct streams remain available but stations do not appear in the directory until the producer chooses Listed. Private stations have no public page, stream or logo; the protected producer player can preview them. Switching to Private disconnects the existing broadcast. Names can change without changing public page/stream addresses; editing the stream address deliberately changes both.

The public shell contains listening controls and no management forms. Its persistent player survives HTMX navigation between public pages, with Pause/Resume, volume and Stop. Public status observers never start broadcasting or count as listeners. Changed content is patched without replacing unchanged artwork or focused controls. Play starts audio explicitly; Pause releases the connection. Public failures use generic listening messages, without source or credential details. Ordinary page navigation and a direct Open stream link remain usable without JavaScript.

Management navigation and writes live under `/producer`, with unchanged authentication/CSRF policy. Producer sign in opens that separate shell through full navigation. Old management bookmarks redirect to their corresponding producer URLs; existing `/streams/<address>` URLs remain unchanged.

### Requests and shout-outs

A public station links to dedicated request and shout-out pages. Listeners choose
an enabled upcoming show, search that show's playable music for a request, and
optionally give a nickname. Shout-outs and dedications are limited to 400 characters;
explicit consent allows the name and message to be read on air. The confirmation
says the submission was sent for review, without promising airtime. If no show
accepts participation, the page says so and offers a route back to the station.

Show settings independently enable requests and shout-outs; existing shows default
to neither. The station's Audience tab provides a filtered, paginated queue with
pending, needs producer review, approved, dismissed, broadcast and expired states. AI review happens
automatically while the selected show is on air. Producers can approve or dismiss
active submissions. Show edits invalidate its outstanding submissions.

The Audience queue has a dedicated Selected recording column with the exact
requested title and artist. Live Up next and Playing now labels come from the
broadcast state. The DJ's request introduction names the listener, incorporates an
optional dedication, thanks them and leads into that exact recording. Location and
other personal details are never invented. A request is fulfilled only after its
introduction and the start of its recording; producers can dismiss it before airtime.

### Public contribution workflow

Each scheduled show unfolds in its own card. HTMX loads details on first opening
and updates a panel inside that card: selected show and airing date, supported
contribution type, form, then a separate thank-you step. The player and live
programme stay in place. Request and shout-out choices are independently enabled
by the show; unavailable future interactions are not displayed. Direct links,
refresh and history restoration render the complete station page at the selected
step. Old or changed occurrences close explicitly rather than selecting another
show. Validation retains listener input. Track searches update only the track
selector, keeping the dedication and name intact. Ordinary links and forms remain
usable without JavaScript.

Opening a show reveals its optional producer-written public description, presenter
photo and airing details in place. The contribution flow stays inside that card and targets the chosen airing, with only the enabled
request/shout-out types. Producer show editing labels the description as public;
DJ editorial prompts and background configuration are never used as public copy.

The public station programme defaults to today in the station timezone. A single
row of day links offers Today, Tomorrow and the following five calendar days. Each
day shows the full programme in time order, including continuous-music gaps and
shows continuing across midnight. Past airings remain visible without contribution
actions. Show details and contribution links retain the selected date and exact
airing. Expansions and contribution steps keep the station URL and scroll position;
day navigation updates only the programme region. Collapsing a show retains its
loaded content and any entered form values. Direct show URLs remain usable as
fallbacks.

All listener HTMX swaps explicitly disable automatic scrolling. Day controls
remain mounted while only the dated programme beneath them changes. Show cards
unfold with a brief animation; contribution steps animate only their own height.
Reduced-motion preferences disable these animations. Searching music returns only
the selector and leaves the station title, URL and other form fields intact.

### Listener schedule preference

The user dislikes arrow dropdowns. Keep every show's time, title and presenter
visible in the full daily schedule. Clicking the show row toggles its details and
contribution workflow directly underneath, with a plus/minus indicator on the right.
Click the row again to close it; omit a separate close button and retain entered
values when reopened.
Use separately spaced time/title/presenter columns on desktop and spaced lines on
phones. Do not use details/summary or chevrons for these show rows.

Use one outer programme panel. Keep day content, schedule rows and revealed show
information unbordered, separated by spacing. Give the contribution workflow one
quiet bordered container with comfortable padding, retained across choices, forms
and confirmation. Card outlines should group distinct tasks, rather than every
content layer. The active show row and expanded details share a soft tinted
background and generous padding to establish the main content area. When a
show has no available interactions, omit the participation section entirely;
retain the show in the schedule and allow its information to be revealed.

Public show details use the presenter photo on the left and description on the right on desktop, stacking on mobile. Bundled and external portraits use their configured image URL; uploaded portraits use the station-scoped public photo route. Clicking the show row toggles its details with a plus/minus indicator, retaining mounted forms. The listener stream card shows the address as one keyboard-accessible copy control with an inline clipboard icon. Clicking anywhere on it copies the address; a checkmark and live confirmation appear only after the clipboard write succeeds. If copying is unavailable, select the address and give manual-copy guidance.

Schedule rows show a compact circular DJ portrait alongside the presenter name, using the same public image address as the expanded details. Omit the photo when none is configured; continuous music has no invented presenter.

Listener track requests search as the user types after a short pause, updating only the show-scoped results. Show up to eight eligible recordings with title, artist and album; provide initial-search and no-match messages. Native radio choices support keyboard and ordinary form submission. Enhanced forms keep the chosen recording in a persistent summary across searches, preserve listener input, and ignore obsolete search responses.

The active contribution workflow has a top-right X labelled Cancel contribution (Close confirmation after submission). It resets only that show’s contribution area to its initial invitation, preserving the expanded show and page position; it never retracts a submitted message.

Within an expanded show, align the contribution area beneath the description column on desktop, stacking it beneath the content on mobile. The initial “Be part of the show” invitation is unbordered and shares the description’s text alignment; the active choices, forms and confirmation retain their padded card.

Schedule show titles keep the same position when opened or closed. Desktop rows share the presenter-column width, gap and horizontal padding with the expanded details, aligning titles, descriptions and contribution invitations. Preserve that alignment even when a presenter has no photo; mobile uses the shared stacked-content left edge.

The producer audience queue explains that review runs during spoken-link preparation. Its results refresh quietly every five seconds, preserving filters, pagination, scroll and focused actions. Tick/X icon actions retain accessible names and tooltips. No-listener and missing-presenter-key notices reflect measured runtime state. AI review failures are logged without listener message text; failed reviews remain pending.

The producer Audience tab uses one bordered table with clear submission types, status badges and accessible icon actions. Status checkboxes refresh only its results on change, defaulting to pending, producer review and approved; clearing every checkbox shows an empty table. Filters remain selected during polling, pagination and review actions, with no page jump. Ordinary GET submission remains available without JavaScript.

### Producer DJ cards

The shared DJ directory uses two presenter cards per row on wide desktop screens,
stacking on smaller screens. Show a substantial portrait and linked show/station assignments and the earliest future airing in its
station timezone. Repeated weekly instances of the same show share one assignment
row. Voice, speech speed, voice direction, model and editorial prompts belong
on the DJ detail screen; omit the Voice & Production section from directory cards.

Each card displays one derived status: Draft for missing required production
configuration, Ready for configured but unassigned profiles, Scheduled for assigned
profiles, or a red On air badge when measured broadcasting state has a current
track and names that DJ. Configuration problems remain visible even on an active
card. Ready confirms saved configuration, not a tested API credential. These
statuses reflect state when the directory is loaded; assignment alone never
implies a live broadcast. Remove Key saved from the directory, retaining credential
management in the DJ edit form.

DJ cards share a fixed height at each viewport size. Keep the portrait and Edit DJ
action fixed in the left column; long assignment content scrolls in
the right column, which is keyboard focusable and labelled by the DJ name. Align
the name and status lozenge on the same top row, wrapping naturally on narrow screens.

Keep the biography on the DJ detail screen; omit it from directory cards.
