# StationOS user experience standards

Status: adopted, 9 October 2026. Apply these rules to every new operator-facing screen.

## Product hierarchy

Stations are the top-level domain. `/producer` is the station overview, with one aligned output row per station and a clear Create station action. Opening a card leads to that station's workspace. All station screens retain station identity. Overview and deeper station screens retain the Stations / station-name breadcrumb above the tabs for returning to their parent. New scheduling, presenters, output and reporting screens must join this station workspace instead of becoming unrelated global pages.

Main navigation contains Stations, Sources and DJs. DJs are shared fictional presenter profiles, managed through a concise list and dedicated create/edit forms. They own a name, selected voice, conversation speed, prompt and voice-direction vibe. Conversation speed uses a labelled slider with a live multiplier display, from 0.25× to 4.00×, defaulting to 1.00×. It saves with the profile and affects newly generated speech. The shared API-key field in Assistant settings is write-only: leave blank to retain, enter a replacement or explicitly remove. DJ forms contain no credential fields. Reuse the standard footer and styled delete dialog. Creation/editing does not itself invoke AI or start broadcasting. Sources serve every station; their imported metadata is a shared implementation detail. Track browsing is opened while adding music to a playlist, retains the target station/playlist and offers a direct return to its Tracks tab. Never ask the operator to choose the same station again when the current workspace already supplies it.

Station tabs follow this order: Overview, Schedule, Audience, Timeline, Incidents, Mixer, Playlists, Settings. Keep daily production and monitoring ahead of occasional configuration.

## Status and information

Show a small number of useful, current facts on station cards. Playlist counts are measured; listener counts come from active broadcast subscriptions, including the app player. Browser probe and playback requests from one app listening session count once. Tiny bare-stream media probes finish after initial audio instead of remaining as duplicate listeners; separate app players and external playback connections remain independent. Dormant stations have a measured zero. Never use zero to represent unknown, invent listener numbers, show a playlist's first track as now playing, or label a station on air without an active output.

Station Overview separates current playback, upcoming programme, listeners and stream health. On phones and tablets, place the logo beside the live listener/status badges and give the description a full-width, fixed-height scrollable region below; retain the desktop layout and keep stream controls below programme/track information. Playback is listener-driven; weekly schedules select the active programme at track boundaries. Show current/next only from the actual producer, with Dormant, Starting, Broadcasting, Retrying and Unavailable states. When those systems exist, render their actual state through their own service contracts. Use server event subscriptions for the status region only; do not repeatedly replace an editing form or reset its scroll/focus. Distinguish offline, loading, stale, failed and unknown states with text as well as colour.

The station directory is the producer dashboard. Show aggregate current listeners,
broadcasting stations and stations needing attention above a stable station matrix. A separate attention rail links exceptions to incidents; on small screens it precedes compact station rows.
Each row prioritises measured output status, audience, programme/DJ, current and
committed next recordings, and the next known programme in station-local time. Show the next programme in a secondary row, with its first two accepted, unaired track choices and artists aligned beneath the Now playing and Up next columns. Read the dated episode without starting preparation; label missing choices and unavailable reads honestly. Episode commits update this preview through the existing event subscription.
Continuous music is identified explicitly. Active incidents, unavailable DJ
capabilities and output failures surface a concise warning linked to Incidents;
dormancy is normal. Keep Open station and Listen available and historical reporting
inside station workspaces. Live disconnection feedback stays local and warns that
displayed information may be stale while reconnecting.

## Navigation and HTMX

Keep the application shell stable. Use ordinary links/forms enhanced by HTMX to replace `#main`, retaining real URLs, page titles, browser back/forward and keyboard navigation. Full requests render a complete document; HTMX requests render a titled fragment. History restoration must recover the complete shell when necessary. Use `HX-Location` for successful writes, avoiding full-page redirects. Use `HX-Redirect` only when a full browser navigation is genuinely required.

New-screen navigation starts at the top. In-place saves and row actions preserve scroll. Every page has one h1 and clear station context. Active global navigation and station tabs use aria-current. Move focus to the updated main region after a screen swap without unexpected scrolling. Ordinary forms must remain usable without JavaScript.

## Selection and editing

For multiple choices, use labelled checkbox lists with search and selected counts. Select/Clear visible affects only the current filtered choices. Searching must never erase checked values. Preserve existing choices that are temporarily absent from the catalogue, label them clearly, and allow them to be unchecked.

Save related selections together, atomically. Selections match any chosen value within each category and must match every selected category and automatically pick up new matching tracks; explicit track exclusions win. Show unsaved state and prevent accidental departure. Never prompt about unsaved changes during the successful save's own navigation. Keep manual selections and exclusions separate from rule edits. Playlists are unordered pools available to the station curator. Do not show sequence numbers or move controls; alphabetical display is for browsing and carries no playback priority.

Catalogue selection applies to the visible page; label its count accordingly. Enable Add selected tracks only when tracks and a target playlist are chosen. Use an indeterminate page checkbox when some rows are selected. Preserve filters, page size and target playlist across page changes and saves. Use one task per tab: playlist Selections, Tracks and Details. Creation uses an Add button and a dedicated form with Cancel, never a card disclosure. Station context supplies the owner automatically. Keep playlist count and duration alongside the title. Put Add tracks and Export in the table toolbar, separate from navigation. Above the table show the result count and labelled 25 / 50 / 100 rows-per-page links, applying immediately and resetting to page one. Use one footer below the data inside its card. Desktop shows labelled Previous/Next and a short numbered-page window including first/last pages and ellipses; phones show Previous, Page X of Y and Next. Highlight the current page, use 44px touch targets, and omit navigation for a single page. Keep filters, sorting and browser history; focus the updated results heading after paging. Do not use page-jump inputs, Go buttons or symbol-only navigation.

Sources follow the same workspace pattern: a concise source list, dedicated Add source page, Overview with measured track/album counts and a refresh action, and Settings for connection fields. Saving or refreshing returns to that source. Add source searches automatically for nearby UPnP music servers without blocking the editable form. Show verified names and server addresses, Use server to fill the name/address, and Already added for existing sources. Selection never saves or imports; the standard Add source footer commits the choice. Keep a manual address field, quiet search feedback, Search again, useful empty/error states, retained input on validation failure and ordinary form fallbacks. Discovery results must not change the URL or replace the form.

## Feedback and recovery

Every successful write gives a short, concrete confirmation. Disable the submitted action during the request and use action-local progress feedback. Background updates must stay quiet: scope HTMX indicators and aria-busy state to the refreshed region. Do not show a global Working banner for navigation, actions or background updates. On validation, server or connection errors, retain the form and operator input; show an accessible error message with a useful next step. Do not replace the whole screen with a raw error response. Bound request sizes and validate on the server even when the browser already validates.

Choose labels that describe the effect: Exclude for a track excluded from dynamic rules, Save selections for a combined rule update. Reserve red destructive styling for actions that destroy operator data. Playlist Details and station Settings expose delete actions. Use the shared styled dialog for delete confirmation and unsaved-change prompts: name the affected entity, describe the scope, focus the safe action, contain keyboard focus, support Escape and return focus on close. Keep normal confirmation-page fallbacks for delete links. Station deletion removes its playlists atomically; shared sources and music remain. Empty states explain what is missing and give one useful next action. Avoid inactive controls for future functionality.

## Form action standard

Place Cancel and the primary Save action together at the end of the form, right-aligned on desktop and in equal-width columns on small screens. Existing entities with deletion show a quiet red text link below the form card, at the bottom left, separated from routine editing controls. Apply this consistently to station, playlist, DJ, scheduled-show and AI-connection edit screens; omit deletion on creation forms. Keep deletion behind the shared confirmation dialog, where Cancel precedes the red confirmation and receives initial focus. Use Save for editing and specific creation/action labels (Create station, Create playlist, Add source, Save selections), rather than Submit. Cancel returns to the relevant parent/overview; unsaved selection changes use the shared discard confirmation. Utility controls such as pagination and refresh keep their compact task-specific actions. Preserve DOM order on small screens.

## Visual system and accessibility

Use tokens in `internal/web/static/app.css`: neutral canvas, white panels, navy text (#10243A), cyan primary actions (#00B9E8) with navy labels and darker cyan links, quiet borders, a consistent radius and spacing scale. The Open Modules logo uses four rounded tiles with a cyan top-right tile; use the shared StationOS SVG wordmark and favicon in both shells. Dark navy playback panels establish priority; use accent colours sparingly. Shared classes cover headings, cards, metrics, notices, buttons, forms, checkbox groups and pagination. Extend those components before adding page-specific variants.

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

Station cards, per-station Overview/Playlists/Settings, playlist Selections/Tracks/Details, dedicated creation pages, single-row pagination, contextual track browsing, responsive components, HTMX navigation/forms/history, successful-save notices, error handling, searchable bulk checkboxes and unsaved-change protection are implemented. Station overview subscribes to runtime changes through server-sent events. Homepage cards use a live event connection and patch only changed content in place; unchanged station nodes, artwork and controls are retained. Stream URLs are stable and editable in Settings; default playlist assignment is managed in each playlist’s Details tab. Playlist cards identify the station default with a labelled star badge in the top right. Set/remove actions refresh only the default control panel, preserving the other fields and page position. The changed badge and action confirm the new default state without an additional saved/removed message. A compact player in the persistent shell survives navigation and history, with Pause/Resume, volume and Stop controls. Pausing releases its listener connection and resumes at the live point. Never expose a seek bar for live radio. Playback starts only on explicit Listen; full reloads end playback.

## Weekly shows

Use a station Schedule tab with a weekly calendar, explicit local timezone and optional show list. Add show opens a dedicated form with a day checklist. Saving creates an independent weekly show for each selected day, atomically; a conflict on any day retains the choices and creates none. Selecting a card opens that day’s show details for individual editing. Use quarter-hour time controls, a shared DJ selector and a station-owned playlist selector. Reject overlaps with a useful message while retaining input. A show may cross midnight. Show deletion follows the common footer/dialog standard and leaves its DJ and playlist intact. Display measured current presenter/show state during playback; never label a planned presenter as actually on air. Starter DJs are editable configuration, seeded once with empty credentials.

## Producer cards

Homepage station cards show measured stream state and listener count, the actually running programme/DJ/time window/playlist, or the configured default playlist when no show is running. Dormant stations show empty current/next states, never a fabricated song or on-air DJ based only on the clock. Current and next rows include title, artist, album, duration and available artwork. During speech the current row shows DJ on air and the speaking presenter’s photo, using the same artwork selection as the persistent player; music restores the album cover. Show readiness, missing DJ credentials, handovers, overrunning tracks and the following show when known. Keep editorial/profile and playlist links contextual, with Listen and Open station actions. Live event updates preserve keyboard focus and stay quiet. Do not replace card nodes or reload unchanged artwork just to update a listener count. Explicitly target the main workspace for card links so a live region cannot capture ordinary navigation.

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

DJ creation and editing group Profile, Editorial style and Voice in one form with a single Save footer. Profile uses the same compact 80px photo editor as station logos: Upload, Generate (when configured) and Remove icon actions, with accessible names, tooltips and 44px touch targets. Keep these actions together beside the preview on phones, with no optional/file-status labels or routine instructions. Generated fictional portraits use the current name, background and vibe; replace only the photo editor and leave the result unsaved until Save. The admin never enters a URL or server path. Selecting a file previews it locally; upload, replacement and removal commit with the other settings. Empty file selection preserves the saved photo. JPEG/PNG inputs are limited to 5 MB and 12 megapixels and normalized for display. Uploaded photos live with the profile and are included in database backups. Invalid uploads retain the existing photo and form; ordinary form errors explain when an upload must be selected again. Settings follow one bounded reading column on desktop and mobile, with quiet separators between groups. Background, prompt and vibe text areas have a consistent fixed 160px height with internal scrolling and no resize handle, keeping the form stable. Conversation speed uses the same 6px label-to-control gap as other fields. Model and provider selection belong exclusively in Assistant settings; DJ forms omit the provider/model explanation and example-vibe shortcut.

## Station logos

Station creation and Settings show an 80px logo preview with icon actions for Upload logo and, when image generation is configured, Generate logo. Both actions have tooltips, accessible names and 44px touch targets. Omit optional/saved labels, filenames and routine instructions. Place an accessible trash action beside Upload/Generate, matching their icon-button styling, and retain shared DJ preview behaviour. Generation uses the current station name and description, updates only the logo control, and leaves a draft until Save; failures preserve the existing logo and other form edits. Local preview changes commit with the form. Empty upload preserves the saved logo; removal restores the bundled radio logo. Limits match DJ photos (5 MB, 12 megapixels). Station cards, Overview and the persistent player show station identity separately from album covers; player identity updates through its existing event connection.

Station names sit beside their logos on listing cards and the station Overview heading, including narrow screens. The floating player uses a dedicated logo column beside its station name/status so branding remains visible without overlapping track metadata. Supported browser and phone media controls receive the selected station logo, actual current title/performer and station name through the Media Session API; metadata follows station/track changes and clears on Stop. Native Play/Pause/Stop actions use the same listener lifecycle as the app controls.


## Track repeat interval

Station creation and Settings include Minimum time between plays (minutes), default 180, accepting whole minutes from 1 to 10080. It applies across the station's shows and playlists and survives restarts and listener disconnects. Help text explains that small pools use their least recently played alternative when necessary. Station cards and Overview show a quiet, measured Repeat interval relaxed notice only while that fallback track is being broadcast. Operators manage duplicate recordings through playlist exclusions; StationOS uses exact track identity.


## Public radio pages

The public homepage `/` lists only stations explicitly set to Listed. Directory cards have equal dimensions, with the logo beside the station name, a full-width fixed-height scrollable description, and Explore station at the bottom left opposite Play at the bottom right. Each station has a direct page at `/radio/<stream-address>` with its logo, description, Play action, actual current/next tracks, running show/presenter, weekly schedule and a copyable MP3 stream URL. Show times use the station timezone, and overnight shows label their next-day end. Scheduled names are programme listings, never an assertion that a presenter is currently on air.

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
shows continuing across midnight. On today’s schedule, completed airings and completed continuous-music gaps are
omitted. The currently scheduled show opens by default with its details and optional
contribution invitation; clicking its title still closes and reopens the mounted
content. Future dates retain the full day with shows initially closed. Show details and contribution links retain the selected date and exact
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

The producer audience queue explains that review runs during spoken-link preparation. Its results subscribe to committed contribution/review/use changes and actual expiry deadlines, preserving filters, pagination, scroll and focused actions. Tick/X icon actions retain accessible names and tooltips. No-listener and missing-presenter-key notices reflect measured runtime state. AI review failures are logged without listener message text; failed reviews remain pending.

The producer Audience tab uses one bordered table with clear submission types, status badges and accessible icon actions. Status checkboxes refresh only its results on change, defaulting to pending, producer review and approved; clearing every checkbox shows an empty table. Filters remain selected during live updates, pagination and review actions, with no page jump. Ordinary GET submission remains available without JavaScript.

### Producer DJ cards

The shared DJ directory uses two presenter cards per row on wide desktop screens,
stacking on smaller screens. Show a substantial portrait and linked show/station assignments and the earliest future airing in its
station timezone. Repeated weekly instances of the same show share one assignment
row. Voice, speech speed, voice direction and editorial prompts belong
on the DJ detail screen; omit the Voice & Production section from directory cards.

Each card displays one derived status: Draft for missing required production
configuration, Ready for configured but unassigned profiles, Scheduled for assigned
profiles, or a red On air badge when measured broadcasting state has a current
track and names that DJ. Configuration problems remain visible even on an active
card. Ready confirms saved configuration, not a tested API credential. These
statuses reflect state when the directory is loaded; assignment alone never
implies a live broadcast. Remove Key saved from the directory, keeping credential
management in Assistant settings.

DJ cards share a fixed height at each viewport size. Keep the portrait and Edit DJ
action fixed in the left column. Keep the DJ name, badge and setup notices fixed
in the right column. Shows & Stations fills its remaining height, with a fixed
section label and a keyboard-accessible internal scroll area for assignments and
next airing, labelled by the DJ and section names. Align
the name and status lozenge on the same top row, wrapping naturally on narrow screens.

Keep the biography on the DJ detail screen; omit it from directory cards.

### Producer assistant

Assistant is available in the main producer navigation, outside the station submenu.
A conversation opened with station context inherits that station without requiring another selection;
it can still manage other stations when explicitly requested. Global conversations
support discovery and station creation. Keep saved conversations, clear New
conversation and Assistant settings actions, and direct links to created/edited
entities. Use the current producer's authority for requested changes without an
extra assistant approval step. Ask for missing information or ambiguous intent.

Keep the live status, task summary and recovery controls above the response log.
Append new chatter, reasoning and action records at the bottom of that log in
chronological order; growing replies must not insert content above the summary.

Keep the compose form mounted while subscribing only the messages/status region.
Event updates must preserve draft text, focus and scroll and must not display the global
Working indicator. Disable Send while the conversation is running; navigating away
does not stop a run. Successful sends get a fresh submission identifier, preventing
repeated form delivery from replaying a turn. Normal forms, direct URLs, refresh
and history navigation remain supported. Links in conversation messages, including View in StationOS, navigate the main workspace and update browser history; they must not inherit the message-region target.

Show concrete saved outcomes and failed actions in the conversation. Never claim
that the whole request rolled back when some actions succeeded. Interrupted runs
remain visible and are not replayed. Assistant settings owns centrally managed provider connections and independent provider/model choices for producer, DJ writing, audience and artwork roles. Speech endpoint/model/voice settings belong to each DJ. Existing OpenAI settings remain preserved. The key field is write-only; blank preserves it. DJ forms have no text-model override; all DJs use the global curation/writing provider and model. Each DJ chooses a speech endpoint and model independently; endpoint addresses and credentials stay in Assistant settings. Do not expose credentials
in conversation history or model tools. Saved configuration is not a tested key.

Assistant responses support Markdown paragraphs, headings, emphasis, lists, links,
quotes, code blocks and tables. Keep message headings below the page's h1 and
contain wide code/tables within the response on small screens. Render stored
Markdown on the server for both normal navigation and status fragments. Raw HTML
and dangerous link destinations are omitted; image descriptions are rendered as
text. Human messages and action records retain escaped plain text.

Long requests continue automatically across model batches. Keep a saved task brief,
completed steps, actionable remaining work and genuine blockers visible in the
conversation. A batch boundary is not a completion or a reason to ask the producer
to repeat requirements. On failure, stop or server restart, offer Continue saved
request with the original instructions intact. Active runs expose Stop assistant.
Incomplete requests must not be presented as complete. Finish actionable configuration
before requesting input needed only for blocked work.

The conversation status explicitly says Working, Finished, Needs your input or
Paused. Active requests show the current activity, total running time, time in
that activity and count of finished checks/actions, including catalogue reads.
A failed live refresh displays a reconnect warning. A saved running request with
no active worker is reported as stopped unexpectedly; Stop makes its saved
checkpoint resumable. Completion means the assistant ended its turn; the reply
and saved checklist remain the evidence of the requested outcome.

Show measured AI input/output token counts while a run is active and keep a
usage record in its action history when it ends or pauses. Counts include cached
input and are not a cost estimate. Quota/billing errors explain what requires
attention rather than suggesting immediate retry. Confirmed temporary rate limits
show a cancellable wait and resume automatically within a short bounded retry
window; longer provider waits remain paused.

New assistant-created stations get a generated logo; new fictional DJs get a
portrait informed by their background and voice direction. Existing artwork is
preserved during reuse and ordinary edits. The conversation shows creation first,
then artwork activity and its outcome. If generation fails, identify the missing
artwork while keeping the saved entity and its link; retry artwork by ID rather
than creating another profile. Assistant settings offers an independent Artwork
model using the shared key and explains automatic low-quality image generation.


### Provider settings

Assistant settings lists named connections and offers dedicated connection editing
pages. Each page has provider type, execution-environment server address, optional
write-only credential and bounded context/output budgets. Save never runs inference.
Delete uses the shared styled dialog, with Delete on the left of the form footer
and Cancel/Save on the right. An endpoint selected for a feature cannot be removed.
Blank credentials preserve saved values; removing one affects only that connection.

Group each role's provider and model. Do not offer cloud fallback; each feature uses only its selected endpoint. Speech and artwork retain
OpenAI choices independently of local text. Local text remains enabled without an
OpenAI key; show specific missing-speech/artwork limitations, preserving unrelated
configuration work. All existing and new DJ profiles use the same global curation/writing model.
Changing it affects all DJs at song boundaries; the current track continues.

Connection checks distinguish server reachability, the exact selected model's
presence in the discovery list, and successful tool/structured-JSON tests. Discovered
IDs assist manual entry; they never replace selected IDs or download models.
Discovery reads model lists without generating or downloading models. Feature model fields offer discovered IDs automatically and retain manual entry when discovery is unavailable. Omit diagnostic capability tests from the setup workflow. Discovery alone never establishes capability.
The producer station overview displays missing-speech notices;
public listener pages do not show provider configuration.


Provider connection editing includes a response deadline in seconds (15–600),
defaulting to 180 for private inference. Explain that the response timer starts
when inference is admitted; producer queue waiting is bounded at 30 seconds, while live DJ work may wait up to the selected response deadline. Stale spoken links are cancelled at song boundaries.
Do not imply a longer deadline guarantees a model's speed. DJ requests disable
thinking only when the provider advertises that option. No deadline or thinking
change starts a broadcast, downloads a model or silently enables cloud fallback.

Show speech and curation readiness separately. Producer station cards and the station overview place small microphone and music-selection icons immediately beside the DJ name, with distinct ready, preparing, speaking and unavailable marks. Provide plain-language explanations on hover, keyboard focus and tap, plus screen-reader labels; never communicate status through colour alone. Do not repeat readiness labels or AI inference notices as visible paragraphs beneath the icons. Status explanations appear only on demand. Use “Speech ready” for a prepared spoken link, and “Curation unavailable · music continues” when next-track selection failed. A curation failure must not hide ready speech or claim the whole DJ is ready. During each track, the DJ chooses one next recording from up to ten eligible candidates before its spoken introduction is prepared. StationOS enforces playlist membership, repeat rules and show timing; listener requests retain priority. Late or invalid AI choices leave normal music running. Curation readiness reflects this selection independently of speech readiness; do not display internal validation details during playback.

### Assistant navigation and setup

Group Chats, Models for features and Manage endpoints, in that order, in a horizontal Assistant submenu using the station-tab component. Keep Assistant as a single main-menu item. Preserve the active tab through HTMX navigation and history. Endpoints use the standard list, top-right Add action and dedicated form; keep advanced budgets collapsed. Show server addresses and write-only credentials on endpoint pages. Save validates the connection and discovers models automatically; failed discovery leaves the form open with an error and does not save the changes. Do not show a Discover models button. Feature models get a separate form with one labelled group per role and exact model IDs, keeping credentials out of that form. Preserve selections when saving only credentials. Sources places Add source alongside its page heading at the top right.

The Audience queue table scrolls horizontally inside its bordered panel on small screens, with a labelled keyboard-focusable scrolling region. Keep pagination outside that region. Preserve the horizontal position through quiet refreshes, filtering and review actions; focused keyboard actions pause automatic replacement, while explicit reviews still update their result.

The AI endpoint directory contains navigation and summaries only. All endpoint configuration, including the built-in OpenAI key, belongs on that endpoint’s dedicated management screen. Key saves and validation errors remain in OpenAI management.

Chats places its primary New conversation action at the top right beside the page heading, using the shared creation-button style.

Large assistant requests remain one user task. Explore libraries through measured database summaries and small targeted searches; do not require users to enumerate recordings or split an approved station configuration into separate prompts. Older catalogue detail leaves working context after a summary; preserve the approved plan and completed-action references. Continue saved request reuses the original requirements.

When configuring a full weekly schedule, the assistant plans a varied presenter lineup across distinct programmes and keeps presenter continuity for recurring editions. It assesses shared DJs per programme, creates original profiles for unmet editorial needs, honours explicit single-presenter or named assignments, and verifies saved assignments before reporting completion.

OpenAI key validation, save-time model discovery and encrypted storage use the same clipboard-normalised key. Discovery errors distinguish rejected credentials, denied model access, rate limits and timeouts without showing credentials or provider response bodies.

The server manages assistant reply space within the configured context budget. Producers do not need to split station requests, write tool constraints into prompts or increase output limits to complete large tasks. A maximum output setting is a ceiling, adjusted per request without changing saved settings. Confirmed output truncation triggers bounded smaller-step continuation while preserving the original request and completed actions; no partial action runs. Existing saved conversations can resume after an update. Unrecoverable capacity/provider failures still pause honestly with the brief intact.

The server also manages action batch size. Valid batches run sequentially and save each outcome. Oversized replies receive bounded smaller-step recovery before any of their actions run, preserving the request and prior work; producers do not need to rewrite their prompt around action-count limits.

A resumed producer setup should recover from measured saved configuration, including missing requested schedule periods and empty music pools. Producers should not have to diagnose repeated tool reads or restate saved requirements. Recovery remains bounded and must preserve completed shows and music choices; unresolved blockers must remain visible.

A producer may name a station in a project-wide assistant chat without first selecting its workspace. Recovery must bind that station and requested work area rather than cycle through global discovery. Use available album titles and artist/track matches with musical knowledge for era requests; absence of a year field alone must not block setup. Report genuinely unavailable music or uncertain matches clearly.

### Assistant conversation

Show human messages on the right and agent responses on the left, oldest first.
Keep actions, returned provider reasoning, task progress and measured activity
inside the corresponding agent response. Provider reasoning uses an accessible
expandable section; never infer missing reasoning or display opaque provider
payloads. Background updates preserve the composer, focus and reading position;
follow new content only when the reader is already near the bottom. Submission
retains input on errors and returns focus to the composer after the response.

### Compact upcoming audio

Upcoming tracks belong on station Overview beside the current audio, not in a separate
tab. Use compact rows with 64px album artwork, title, performing artist, album and known
duration. Show only the runtime's actual selected next item; never fill a queue from
playlist browsing order or a speculative plan. Mark selections as revalidated before
playback. Dormant stations explain when selections become available. Keep the following
show in the programme band. Live overview reconciliation preserves unchanged images,
focused controls and reading position. Old Upcoming bookmarks lead to Overview's Up next
section.

### Operational notices and live-region navigation

Use Timeline and Incidents tabs for station history and incident navigation. Do not
repeat these as a shortcut row inside the station overview or show a zero-incident
status. Surface only active incidents and history-recording problems as notices.
Navigation links inside a live region explicitly inherit a main-workspace target,
separate from the region's live-update target, so clicks cannot nest a page inside a card.

### Producer broadcast history

Timeline is a compact broadcast-history table: when, recording/activity, show, presenter,
playlist, duration and outcome. Keep database IDs, source object paths, byte counters
and routine selection diagnostics out of visible history and its producer CSV. Retain
spoken text and useful failure explanations. Recording cells show title, performing
artist and album; omit recording-credit disclosures. In-progress items say In progress,
not Unknown or a periodically changing emitted-byte duration. Use names captured at
transmission; label any current-name fallback on older records honestly. Group recurring
show editions by their saved show name, with one filter including every weekday.

A refresh notice must compare the same filtered page the producer is reading. Ignore
byte checkpoints, unrelated broadcasts and runtime preparation/listener state. Completion
and interruption are meaningful changes; say Broadcast history updated rather than
claiming every update is a new event. Timeline history survives log rotation; low-level
fault diagnosis belongs in the server logs.


Keep broadcast history results and their pagination footer inside one card, with filters
in their own compact card above it. Reuse the shared results toolbar and footer.
Preserve date/show/DJ/type/outcome filters and page size. Use a stable history anchor
while browsing older pages so new broadcasts do not move records between pages.
Applying filters resets the anchor.

On phones, broadcast history reflows into stacked entries without horizontal scrolling.
Use accessible type and outcome icons without a separate key. Keep optional filters
collapsed initially at every screen size, preserve active filters through pagination, and keep current and
upcoming audio sections free of inherited section margins.

History answers what aired, when, under which programme and whether it for review.
Show one date with a compact start/end time range; include the end date when it crosses
midnight and zone names across clock changes. Keep full timestamps in accessible labels,
tooltips and CSV. On tablets omit album, playlist, duration and end time from the table;
on phones use compact entries showing start date/time, title, artist, show/presenter,
outcome and useful failure explanations. Keep show context aligned with the title.
Phones hide page-size choices and use simple Previous/Next navigation;
the desktop view and CSV retain the full data.

The producer explicitly dislikes arrow dropdowns: do not use details/summary or chevron disclosures for producer content. Keep episode preparation errors visible inline, without a “Details” toggle. Show transmission spoken text in full,
inline in its speech entry. Use a plain Filters + / Filters − button for optional
history fields, with an accessible expanded state and visible fields without JavaScript.

History search places the clear-all action as an accessible X inside the right edge
of its input, rather than a separate Clear text link.

History updates use a 44px refresh icon at the left of the results toolbar. Keep it
muted and disabled until a meaningful update exists; enable and highlight it with
an accessible announcement when one arrives. Refresh only the results card, retaining
filters, navigation, URL and reading position; replace the subscription baseline with it.

Do not describe operational errors as Recorded. Observed history events use their
actual producer status: pale red Error for playback/station failures, amber Warning
for degraded operation, green Recovered for recovery, and neutral Station activity
for routine events. Keep explicit labels and different icon shapes alongside colour.

Use distinct outcome shapes: recovery arrow with tick for Recovered, circled X for
Error, warning triangle for Warning, and plain tick for a completed transmission.
A recovered event must never retain a warning icon, even if its stored severity
reflects the original incident.

Incidents use one results card and the shared toolbar/pagination, with latest occurrence,
problem/effect/action, active or recovered status, and producer acknowledgement in a table.
Stack rows on phones; keep explanations visible without disclosures. Acknowledgement
records review and never implies recovery. Show a small labelled warning icon beside the
Incidents tab only while active incidents exist, including acknowledged but unresolved
ones. Subscribe the indicator independently so navigation does not replace the workspace.

Use a 44px eye icon button labelled Mark as reviewed for incident
acknowledgement; show Reviewed afterwards. This never removes or resolves an incident.

Incidents default to Active (unresolved problems, including reviewed ones). Needs review
shows unreviewed incidents whether active or recovered; All incidents retains recovered
history. Review and recovery are independent. Preserve the current filter and page
when marking reviewed. Do not offer deletion in the normal producer workflow.

Incident severity is conveyed by its status icon and colour, with an accessible label
and tooltip. Do not repeat warning/critical as a separate text line below the badge.

Keep incident rows compact: latest time/status above the problem, with transmission
link and review action sharing a footer. Use an eye for review and eye + Reviewed
for the completed review state. Show occurrence counts only above one, omit repeated
effect wording, and show repair guidance only for active incidents.

Timeline uses the Incidents tab indicator rather than repeating an active-incident
banner. Keep actual transmission-history recording failures visible separately.

Place Active / Needs review / All incidents inside the incident results card, above
its toolbar, so filtering, rows and pagination form one clear section.

Station data tables share the Audience table outline: a 1px divider-colour border,
the standard rounded corners and a white surface, clipping row backgrounds cleanly.
Keep scrollable tables scrollable and give stacked phone rows an inset inside the outline.

Retain the same table outline around empty result states, including No active incidents.


### Live updates

Use the event subscription model in [live-updates.md](live-updates.md) for all
background screen changes. Never introduce periodic HTMX triggers, timed fetches,
or a polling fallback. Transport heartbeats stay invisible. A disconnected stream
shows a quiet local reconnecting state and recovers with a fresh snapshot.
Preserve editing forms, keyboard focus, artwork, horizontal scroll and reading position.
Timeline and Incidents subscriptions enable their refresh controls; they never
replace history rows until the producer requests it.


Station navigation retains its horizontal scroll position when changing tabs on small
screens. Keep the current tab visible on direct links, reload and history restoration,
without moving the page to reveal it. Only the current tab uses the selected accent and
underline; hover must not resemble a second selection or remain highlighted on touch.


Playlist Refresh rechecks the saved genre/album selections against the imported
catalogue; it never imports a source or reinterprets an Assistant brief. Report new
eligible tracks and distinct source/album pairs since the last refresh or membership
edit, including a clear zero-additions result. Overlaps count once and exclusions win.
Existing dynamic rule matching remains automatic. A new playlist/selection change
establishes its comparison baseline; upgrading establishes existing members without
claiming them as new. Keep refresh manual; source import does not invoke it.

Excluded tracks remain in the Tracks table with a pale red background, an explicit
Excluded label and Re-include action. Keep their metadata/snapshot even if the source
is unavailable. Exclusions do not contribute to eligible track counts, durations or
playback; pagination includes them as editable rows. Restoring an unavailable saved
track retains its honest Unavailable state until its source returns.

Saving playlist genre/album selections also refreshes in the same transaction and
reports new tracks/albums in its saved confirmation. Preserve manual inclusions and
exclusions. A failed save or refresh must roll back both the selections and baseline.

### Shared HTML/CSS component ownership

See [ui-components.md](ui-components.md) for canonical component markup and
responsive contracts. Semantic sections are visually neutral; use explicit panels
for distinct tasks. Routine creation and editing forms share the same footer.
Audience, track and history results share toolbar/pagination components; rows-per-page
choices remain available on narrow screens. Reuse semantic feedback tokens and
shared badge/action anatomy before introducing feature-specific rules.

Headings, breadcrumbs and linked workspace tabs use the shared templates in
`components.html`. Apply the same title/metadata/action layout across producer
screens. Workspace tabs keep their own horizontal scroll position and reveal the
active link across station, source, playlist and Assistant navigation. Breadcrumbs
use labelled ordered navigation and retain links to parent workspaces.


## Rich playlist selections

Selections provides searchable checkbox groups for Genres, Albums, Artists,
Composers, Years and Decades, with visible-select, clear, unsaved-state and atomic
save behaviour. Values within a category are alternatives; selected categories
combine automatically. Pop + 1980s means 1980s pop. Years and decades share one
date category. Empty categories impose no restriction; no rules means no automatic
tracks. Manual additions stay separate and exclusions win. Explain this above the
choices rather than offering a separate Combined rules selector or creation link.
Missing saved values remain checked and explicitly labelled.

Previously saved advanced restrictions appear only where they exist, with edit and
remove actions. They further restrict the selected categories; any one saved advanced
restriction must match. Ordinary checkbox saves preserve them. Keep the existing
form validation and ordinary-form fallback for editing these saved restrictions.
Year and decade selections prefer the source’s original release date and fall back to its album release date, which may describe a reissue; missing dates do not match date selections.

Source Settings offers Remove source below the form, using the shared confirmation dialog and an ordinary confirmation-page fallback. Explain that removal affects every station, keeps external music files and playlist selections, and makes recordings from that source unavailable. Successful removal returns to Sources with a confirmation.

### Station music sources

Create station and Station Settings include a Music sources checkbox list, selected count and Select all/Clear controls. Save commits source choices with the other station settings. Show unsaved source choices and use the shared discard dialog when leaving; successful Save must not ask to discard. Place Music sources in its own card using the shared panel style, with one concise instruction and no additional help disclosure. Multiple sources exposing the same music can include duplicate recordings; new sources require explicit selection, and no selected sources means no music. Existing stations retain access to all sources present at upgrade. Source changes apply to station playlist choices and future track selection; saved rules and manual selections remain, with unchecked-source tracks unavailable. Music files are untouched.


## Episode readiness and running orders

The schedule selects a dated week. Each occurrence has a text readiness label and
neutral Scheduled, blue Preparing/Updating, green Ready, amber Delayed (retrying),
or grey Cancelled background.
Readiness is editorial preparation, independent of live broadcasting. Plans become
ready automatically; producer review is optional. Clicking a show opens its running
order and show details in the main content area through HTMX navigation, with a dated URL and a link back to the selected schedule week.
Keep its show settings available as a separate link. Show estimated track starts,
performers, albums and durations. Producer notes belong to that dated episode, not playlist membership. Track choices
are agent-driven: the default episode screen shows the upcoming queue in the same
table style as Played tracks, with expected start, recording, performer, album,
duration, selection source and acceptance status. Only live runtime can label a track
Next; following choices use Upcoming. Retain the precise acceptance state in the
accessible label and tooltip. Legacy origins are Not recorded.
Do not show Keep/Exclude controls. Live readiness updates preserve
calendar scroll/focus and never overwrite the producer form. Offer an explicit
running-order refresh, protect unsaved changes and retain input on validation.

Track-selection outcomes appear as plain inline Timeline entries, separate from
aired music: DJ selected, automatic selection (including a choice not yet ready),
or approved listener request. Record only results applied to the current queue,
with recording, presenter and playlist context. Queue selection is not proof of
airtime or an invented account of the agent's reasoning. Routine outcomes do not
create incidents; stale preparation results are ignored.

Overview incident notices use the same unreviewed active incident count as the
menu indicator. Hide the review notice once all active incidents are reviewed;
reviewing does not assert technical recovery or remove incident history. History
recording health warnings remain independent.

The DJ keeps a rolling two-track horizon: current music plays while the next is
ready and the following is being chosen. This does not trigger full-show planning.
A standard show handover is neutral activity, never a warning; editing the same
dated episode's template must not produce a handover.


### Accepted playback queue

While a station has listeners, prepare the immediately following episode's first two
accepted recordings during the current programme. Do not prepare later episodes.
The background planner uses the same playlist, station rotation and performer/album
spacing rules as playback and adds one recording per request behind the accepted queue.
A delayed provider response cannot replace the listener's displayed current or next
recording. Commit a usable next recording before publishing current music; reserve an
automatic choice if DJ preparation is delayed. Speech uses that committed recording.
On advancement, replenish the buffer behind the next recording. Old unaccepted plans
must be replaced, rather than counting toward readiness indefinitely.
If a scheduled playlist becomes empty, continue with an available default playlist or
the last playable pool, identify it as continuous music and record the failure. With
no alternative eligible recording, reserve the current playable recording again rather
than publishing music with an empty next slot. This cannot recover inaccessible audio
sources or a failed database; those failures remain visible.


### Compact episode tables on phones and tablets

Upcoming and played track tables retain compact rows below 1000px. Group title,
performer and album together, with status and duration beside the details and time
and selection source on one small metadata row. Do not turn every table cell into
a separate labelled block or repeat Recording/Performer/Album labels for every row.
Keep all producer information available, allow long names to wrap at word boundaries,
retain accessible table headings and use centred text inside short Next/Upcoming
lozenges. Desktop headings must not break words. Episode direction contains only
prompt editing; the live queue belongs on the default episode screen.

Both upcoming and played episode tracks use the same selection-source icons and accessible help. Preserve selection provenance in the actual aired receipt; historical missing sources remain explicitly unknown.

Timeline activity icons on the left identify the event and remain neutral: DJ microphone, music, automatic selection or handover arrows. Right-hand icons describe outcomes: green tick for success, red cross for failure, amber triangle for a delay/warning. Routine completed handovers use arrows on the left and a success tick on the right. Unrelated observations remain neutral. Keep accessible outcome labels specific; never replace an outcome icon with an activity icon.

The Mixer places Auto level beside the music and DJ faders as a two-position rocker switch with an indicator lamp, visible On/Off labels and native keyboard/screen-reader switch semantics. It saves automatically, defaults off and applies at the next track. Keep explanatory copy below the channels rather than squeezing it into a narrow strip.


### Action feedback and playlist track search

Playlist Tracks searches the whole playlist by title, performing artist, album and
composer before pagination; combine search words across those fields. Keep excluded
recordings searchable. Preserve query and page size through paging, exclusions and
re-inclusion. Exclusion/re-inclusion updates only the affected HTMX row, restores
its action focus without scrolling, and updates independent playlist totals. The
red row and Excluded label are the confirmation; omit redundant success notices.

Routine form saves communicate through the Save control, as specified below.
Other action outcomes use dismissible, fixed status notifications above the player,
rather than top-of-page banners. Success notifications
expire after seven seconds unless hovered or focused. Errors remain until dismissed
or superseded, never force the producer to scroll, and form validation stays by its
form. Preserve operational warnings in their relevant views.

Routine edit forms communicate through their Save control: disabled unchanged, enabled when edited, Saving… during submission, disabled ✓ Saved after success, and enabled Retry save after failure. Re-editing restores the normal Save label. Do not insert Saved text into button grids or add routine success pop-ups. Keep error guidance persistently beside the fields/actions. Buttons retain their normal GET/form fallbacks without JavaScript.


## Source album browser

Source Overview groups measured library totals and a compact Refresh library action
above one album results card. Show 12 albums per page in a responsive 1/2/3/4-column
cover grid, with titles, supplied album-artist credits and track counts. Missing
artwork uses a neutral placeholder. Open an album on its own page with a concise
artwork/details card and a paginated tracks table card (25/50/100 rows). Keep source
scope, album and return-page context in GET URLs. HTMX pagination replaces only
the results card and focuses its results heading; browser history and ordinary
GET navigation remain available. Artwork uses the same authenticated, bounded,
source-validated proxy as broadcast artwork; never expose remote server URLs.

Search fields place their clear action inside the text box as an accessible X icon,
never a separate Clear search text link. Search submit buttons stay compact beside
the field at desktop, tablet and mobile widths. Preserve the page-size setting
when clearing a query; use the same HTMX results region as the search submission.

Table headings and ordinary metadata should wrap at spaces, without splitting
words. Give metadata columns enough width and allow the table's own horizontal
scroll on mobile. Keep fixed playlist column widths stable during row updates.


### DJ speech provider voices

With Chatterbox selected for Speech, DJ creation/editing shows a Chatterbox voice
selector populated by the selected endpoint's catalogue, including Server default.
Keep each DJ's choices separately for each Chatterbox connection, preserving its
OpenAI voice when switching providers. Label retained speed/vibe controls as OpenAI
settings. Failed discovery shows a local warning and retains the saved selection;
a removed voice remains selected and labelled unavailable. Allow unrelated edits
without forcing a replacement. Validate new selections against the active endpoint,
retain input on errors, and reject a stale form if its speech provider changed.
Discovery runs only when opening a DJ form or validating a selection, never through
background polling and never by generating sample speech.


### DJ-owned speech settings

Each DJ's Voice section selects a shared speech endpoint. OpenAI shows model,
voice, conversation speed and direction; Chatterbox shows its fixed Nano model
and discovered voices. Hide unsupported controls while preserving their values.
Changing endpoint previews fields without saving, generating audio or resetting
editorial input, uploads, focus or navigation. Keep unsaved preferences when
switching and commit them atomically with the DJ. Provide normal-form fallback
through Update voice options; JavaScript enhances selection to update immediately.
A preview must never show Saved or reset the form's unsaved baseline.

Shared endpoint addresses and keys remain under Assistant → Endpoints. Remove
Speech from global Models. Existing DJs inherit the previous global speech
endpoint/model on upgrade; new DJs default to OpenAI. Readiness reflects each DJ's
selected endpoint, not the legacy global speech route. Protect endpoints in use
by DJs from deletion or changing provider type.
