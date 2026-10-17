# Development status

Updated 9 October 2026.

## Implemented

- Independent Go module and clean domain/adapters/composition structure.
- Retro Radio read-only inspection and extraction boundary before implementation.
- Station creation/editing, shared source configuration/editing and stable identifiers.
- Station-owned playlists with dynamic genre/album unions, manual inclusions/exclusions, deduplication and unavailable-item snapshots.
- Rich catalogue details, genre/album filters, sorting, 25/50/100 page sizes, numbered navigation and page jumps preserving filters.
- General AudioItem kinds, optional metadata, resource alternatives and origin.
- Source-owned provider contract, capabilities and explicit permission restrictions.
- UPnP/MinimServer ContentDirectory adapter: description, bounded SOAP/DIDL, tree browsing, get and playable-resource resolution.
- Rebuildable local catalogue with literal search, deterministic pagination, stable IDs, atomic imports, idempotence, source revision checks and orphan cleanup.
- SQLite WAL/foreign keys, embedded numbered/checksummed migration and integrity checks.
- Server-rendered Dashboard/Stations/Sources/Catalogue/Playlists with vendored HTMX, bounded/CSRF-protected writes, HTML escaping and optional administration authentication.
- JSON logging, liveness/readiness and versioned status API.
- CLI catalogue sync/clear and integrity commands, embedded timezone/web assets, non-root Docker image, Compose, Makefile.

## Partial / deliberately limited

- Sources are configured by known device-description URL. Automatic SSDP discovery is not included.
- Source imports reconcile a complete bounded snapshot. Incremental refresh, background jobs and persisted import health/progress are not included.
- Restrictions are exposed by the contract; no audio-manipulation/broadcast path exists yet to enforce them.
- Catalogue metadata resources are discoverable and resolvable through the provider contract. There is no audio preview or playable station stream.
- This is a trusted-host administration foundation; multi-user authentication/RBAC, public internet deployment and proxy-aware TLS handling are not included.
- Migration tests cover empty databases, reopening, immutable checksums, newer-schema rejection, foreign keys and upgrades from frozen v1/v2 databases to v3.

## Planned next

Deterministic programming policy → programme log → execution engine → local audio output, with injected clocks, failure recovery and current/next audit status. After that works, ahead-of-time AI production behind text/speech interfaces. Follow with presenters, information providers, outputs, operational metrics and commercial scheduling.

## Explicitly out of scope

Advertising marketplace, commercial music APIs, sophisticated transitions/crossfades, receiver control, legacy radio XML, live takeover, autonomous AI, multi-station commercial operations and a JavaScript SPA.

## Verification

Verified on 9 October 2026: build, `go test ./...`, race detector, `go vet ./...` and formatting checks passed. Docker image `stationos:dev` built successfully and a temporary non-root container started from a clean database, became healthy, passed the integrity command, served the dashboard/catalogue/assets and accepted station/source configuration via real HTTP forms. Safari rendering and filtering/pagination were verified against an isolated copy of the real 731-item catalogue. Historical foundation coverage was 81.7%; this is not a current completeness claim. Normal tests use local fixtures, SQLite temporary databases and local fake HTTP servers. No test requires MinimServer, an AI key, internet, Icecast or commercial credentials. UPnP contract tests exercise the actual adapter through catalogue import and search. Persistence tests check rollback, idempotence, orphan cleanup, configuration preservation and stale-source revision rejection. Web tests cover the station → source → import → catalogue journey, forms, fragments, assets, escaped metadata, authentication and failures.

Live MinimServer metadata browsing and full catalogue import have now been verified for StationOS (731 items on 9 October 2026). Audio playback acceptance remains outside this foundation because it has no playout/output implementation; this is distinct from Retro Radio's device acceptance.

## Browser form correction — 9 October 2026

Changed the response Referrer-Policy from `no-referrer` to `same-origin`. Native form navigations can serialize their Origin as `null` under `no-referrer`, which conflicted with the existing origin guard. Same-origin submissions retain their identity; null, foreign-host, wrong-port and wrong-scheme origins remain rejected, as do malformed origin URLs. CSRF form tokens remain mandatory. Added an HTTP regression test for the emitted policy, successful station creation and rejection paths.

## MinimServer catalogue correction — 9 October 2026

The first real-library import exposed combinatorial virtual folders in MinimServer's facet menus, hitting the container safety limit. Added an optional source-owned catalogue-root contract; the UPnP adapter detects MinimServer from manufacturer/model metadata and verifies its unfiltered all-items container. Only that complete enumeration is indexed. Generic UPnP sources retain bounded tree traversal. Pagination and idempotent-import tests include a virtual-menu fixture that fails if the importer enters facets. Audio remains on the source server; only metadata is imported.

Live verification of the corrected import: 731 items indexed in approximately 0.3 seconds, confirmed through the running HTTP catalogue/status endpoints. Station/source settings were preserved. All Go tests, race checks, vet and formatting passed.

## Shared catalogue and station playlists — 9 October 2026

Sources are now shared across stations. Each station owns any number of playlists drawn from the same catalogue. Genre/album selections automatically include new matches; exclusions win and manual choices/order survive clear/rebuild. Album selection uses source plus album title; duplicate titles within one source are grouped. No scheduling or playback is claimed.

Upgrades from both previous schema versions preserve station/source identifiers, revisions and catalogue rows. The real database was backed up online and the copy upgraded for browser checks; test changes were not copied into the live database. Restart the existing application to apply the migrations. Added tests cover cross-station selection isolation, overlapping rules, new matches, missing items, exclusions, order persistence, transactional failure, rule bounds, form journeys and safe redirects.

## Station-first user experience — 9 October 2026

Homepage station cards lead to dedicated Overview/Playlists/Settings workspaces. A consistent sidebar, breadcrumbs, tabs, cards, forms and feedback system replaces the old global dashboard. Current/next/listeners/output have explicit unconnected states; playlist counts are real. Shared catalogue selection retains its target station/playlist. Genre/album rules use searchable checkbox lists with counts, select/clear visible, unsaved-state protection and one atomic save. Ordinary forms work without JavaScript; HTMX navigation/writes retain the shell, URLs, titles and history, with error and success feedback.

Server tests and race checks cover station routes/context, HTML escaping, titled fragments/full history restores, HX-Location, atomic batch rule replacement/rollback, clearing rules without losing manual choices, and preserving unavailable choices. Safari checked the real copied catalogue and combined genres/albums; a native save updated the playlist count and confirmation in place. Browser checks found and corrected an extra prompt on save navigation. Standards in docs/user-experience.md and AGENTS.md guide future work.

Playlists are unordered candidate pools for a future AI curator. Browsing is alphabetical; saved ordering and sequence controls have been removed.


## Shared station streaming (9 October 2026)

Added playlist assignment, editable name-derived stream addresses, public device-compatible MP3 streams, live listener/current/next reporting, and a persistent HTMX app player with Pause/Resume, volume and Stop. A single shared station producer performs conversion and paced delivery; bounded queues evict slow listeners. Zero listeners means no source fetch or encoder. FFmpeg is now a required serve runtime, installed on the development Mac and included in Docker.

Tests cover concurrent admission/fan-out, late joins, dormancy/reconnect, slow eviction, capacity, pool refresh/exclusions, ownership/deletion, stable/unique/editable addresses, frozen v1–v4 upgrades to v5, protected management/public stream routes, HEAD and live-range compatibility and real generated FLAC → MP3 → PCM decoding. Real MinimServer audio played in the app and survived Sources navigation and browser Back. Two external HTTP listeners reported two connections and received matching overlapping stream bytes; captured output decoded successfully. Race detection, vet and formatting checks pass.

The selector currently shuffles the unordered pool; AI curation and deterministic scheduling remain future work. One assigned playlist per station is supported in this milestone. Browser/device buffering may delay audible playback relative to delivery status. Gapless transitions, crossfades, ICY metadata, physical-device acceptance, distributed outputs and full hardware capacity benchmarking remain open. Earlier foundation sections describe historical milestones.


### Safari local playback compatibility fix

Reproduced Safari failure on the local port 8080 application. Request tracing confirmed its initial `Range: bytes=0-1` received 416 before any audio subscription. Live GETs now ignore Range and return the ongoing 200 MP3 representation, retaining Accept-Ranges:none and no invented file length or Content-Range. Safari playback succeeds with this response. Retry also reconnects when a failed media element reports paused=false. Regression coverage uses actual HTTP streams with zero-offset probe and nonzero ranges, plus HEAD without playback.


## Shared DJ profiles (9 October 2026)

DJs joins the top-level menu with a concise list and dedicated Create/Edit forms. Profiles store a fictional name, OpenAI writing/curation model (presets and custom IDs), selected voice, editorial prompt, voice-direction vibe and an optional encrypted API key. The supplied voice example can be inserted explicitly. Saved credentials are never rendered; blank edits preserve them, replacement/removal is explicit, and delete uses the shared styled dialog and form footer. Migrations 6 and 7 preserve all existing station/source/playlist/catalogue/broadcast data; frozen v5/v6 coverage supplements the older upgrade fixtures. Tests cover encryption, restart/key-loss handling, credential tampering, management authentication, CSRF, validation, safe HTML, HTMX and ordinary form flows. Browser verification covers create/edit, custom model persistence, example insertion and the delete dialog. Profiles are configuration only; station DJ assignment, AI curation and speech generation are not connected yet.

Voice dropdown includes all 13 built-in OpenAI TTS voices. Cedar is the initial selection. Voice selection persists independently of the model and vibe. No speech API calls are made in this milestone.

## Weekly shows, AI DJ curation and starter presenters — 9 October 2026

Added station-local recurring weekly schedules, a calendar/show list and dedicated Add/Edit/Delete forms. Each show selects a shared DJ and station-owned playlist; overlaps and cross-station choices are rejected transactionally. Default playlists cover gaps. Playback finishes the current track before resolving the next show and keeps subscribers on the same stream during handover. Current/next programme and presenter status accompany measured runtime metadata.

DJs curate eligible tracks using prompt, musical expertise and metadata including durations. Background Responses/speech adapters prepare plans and voice-directed MP3 assets, plus current/next-song links while tracks play. Timing/eligibility checks and duration fitting stay local; absent/failed/late AI uses music fallback. Prepared plan/audio caching is bounded and separate from execution. Full deterministic event logs and durable execution audits remain unimplemented; brief source/codec gaps and metadata inaccuracies prevent exact timing guarantees.

Migration 8 adds weekly show configuration and prepared-asset storage. Migration 9 adds Alex Bright (Pop), Miles Mercer (Jazz) and Clara Vale (Classical) once, with distinct prompts, voices and vibes and no keys. Existing profiles, credentials and later edits/deletions are retained. Frozen upgrade fixtures now cover released versions 1–7. Normal, race, vet and formatting checks include schedule boundaries/DST, owned selections, deletion constraints, seed persistence, current-track completion and shared handover, API payloads with fake responses, and real FFmpeg decoding of joined MP3 speech segments. Live OpenAI requests are not part of these checks.

## Producer station cards — 9 October 2026

Homepage cards now expose the running show/DJ/time window/playlist, or the default playlist, plus actual current/next track titles, artists, albums, durations and artwork. Listener counts, DJ readiness/failure/missing-key state, overrunning tracks, handovers and following shows help producers assess a station at a glance. Preparation results update runtime status during the current track without restarting playback. Quiet polling retains keyboard focus; card links explicitly target the main workspace rather than inheriting the polling target.

Artwork is proxied only for current/queued metadata through the source-owned DNS-pinned transport. Responses are raster-only, bounded to 4 MiB/eight seconds, limited to four concurrent requests and privately cached. The existing image CSP stays unchanged. Tests cover dormant/scheduled/default states, local timing, metadata escaping, authenticated artwork, bad formats/size/redirects and live preparation status. Browser checks use explicitly named Preview stations and synthetic playback/artwork; real configured-source artwork retrieval was also verified (JPEG, 423,544 bytes). No AI calls were needed.

## Chatter transition latency

Chatter remains prepared during the current song, with the queued song's metadata. Ready chatter now follows music without waiting for source/FFmpeg cleanup or cancelled AI workers. The next song is source-resolved and decoder-primed during current playback with bounded read-ahead. Generated speech edge silence is trimmed without removing internal pauses; old cached speech assets use a new versioned key.

Verified with two listeners sharing ready chatter and primed music while old-source cleanup is deliberately blocked, cancelled/late AI which cannot stall music, real FLAC-to-MP3 preloading/decoding, speech edge trimming and preserved internal pauses. No live OpenAI calls or production playback were needed for these checks.

## Performer and composer attribution

Fixed a UPnP import bug which flattened repeated `artist` fields and ignored roles, allowing a Composer entry to overwrite the performer. Role-aware import keeps performer, composer, album artist, conductor and ambiguous creator distinct. The DJ receives both current and next recording credits and role-aware curation candidates.

Verified against three actual source tracks: Slovak Radio Symphony Orchestra is the performer of Christopher Gunning's Concerto for Piano and Orchestra; Christopher Gunning is separately preserved as composer. Regression checks cover both role orders, multiple/deduplicated performers, author/composer variants, composer-only and unknown credits, JSON catalogue persistence and the actual DJ request bodies. No live OpenAI request was needed.

Actual pop checks also exposed AlbumArtist overwriting track performers: “(Feels Like) Heaven” changed from “Various artist” to Fiction Factory, and “(I've Had) The Time of My Life” from compilation-level “Various Artists” to Bill Medley & Jennifer Warnes. The corrected importer preserves compilation album credits separately; these examples did not supply a composer and the DJ is instructed not to invent one. Added pop compilation and multi-songwriter regression cases.


## Quiet event-driven homepage

Removed the three-second homepage card polling/replacement. Broadcast and configuration changes trigger server-sent events; unchanged rendered content emits no update. Client reconciliation preserves unaffected station nodes, unchanged covers and focused controls. The event observer does not start audio or count as a listener.

Backend tests verify quiet unchanged audio frames, coalescing, listener changes, view deduplication, authentication and disconnect cleanup. Real-browser synthetic-station checks recorded zero mutations in the unchanged station, zero card replacements and zero artwork replacements while listener counts changed; focused Listen survived a cross-view update. HTMX navigation and Back restored working live updates with one homepage heading. No live music source or OpenAI call was used for the preview.


### Persistent player metadata

The shell player shows the actual current and next titles and performing artists, with DJ on air during speech. A station-specific authenticated event connection updates only changed metadata and remains active across HTMX navigation. It does not start audio or count as a listener; switching stations replaces the connection and Stop closes it. Empty runtime states show Nothing playing and Not queued. The layout wraps for smaller screens.


## Hardware-radio buffer reserve (10 October 2026)

Investigating mid-track cutouts on a Pure radio through an HTTPS proxy exposed insufficient buffering: delivery previously started at the nominal playback rate, late listeners received no recent-audio burst, and a supply stall over one second discarded the catch-up clock. The serve command now defaults to a bounded five-second startup/join reserve (`-stream-buffer`, 0s–5s), with a shared recent window and atomic replay-to-live admission. The pacing clock repays short deficits without allowing unbounded recovery bursts. FFmpeg output is normalised into 4 KiB blocks through a cancellable eight-second read-ahead channel for each current/preloaded track. Source stalls and slow-client eviction are logged. Audio and listener queues remain bounded; the same programme serves all clients.

Regression checks cover paced startup bursts, bounded source fetching, partial reads/EOF/errors, parent cancellation, late joins without missing or duplicated bytes, and uninterrupted two-listener delivery through a controlled mid-track input pause. Generated FLAC is encoded by real FFmpeg; startup and late-join MP3 buffers decode successfully. Physical Pure radio/proxy acceptance still needs verification after deployment; prolonged network/source outages can exhaust the finite reserve.


## DJ backgrounds and portraits

Implemented editable fictional backgrounds, picture URLs and portrait previews, with generated bundled portraits for Alex Bright, Miles Mercer and Clara Vale. Backgrounds reach both show preparation and per-track links; existing editorial, factual-credit and brief-link constraints remain. Profiles are not persistent presenter memories and do not retrieve news or verify new music facts. Upgrade migration preserves credentials and operator configuration.

## Producer transmission history and operational views — 16 October 2026

Added durable shared music/speech observations with immutable credit snapshots,
15-second byte checkpoints, completion/interruption/failure outcomes, restart recovery,
production start/stop and handover observations. Preparation failures and missed speech
boundaries, source/decoder failures, supply stalls and recoveries create grouped incidents;
slow listener eviction is recorded without creating an indefinitely active incident.
Producer acknowledgement does not resolve an incident. Exact generated link text and
prepared introduction/handover text accompany speech where supplied by the producer.

Producer cards and station Overview expose history health, active incident count and
latest activity. Station Timeline provides literal search, bounded pagination, station-local
date/type/outcome/show/DJ filters, event details and bounded CSV export. Upcoming separates
transmitting/selected/preparing audio from schedule periods and default gaps. Incidents
show first/latest occurrence, repeat count, impact, next action and independent acknowledgement
and recovery. Historical views show a quiet refresh link rather than replacing read content.

The initial release observes server emission, not listener reception. A crash may lose
queued observations or progress after the last checkpoint; unfinished persisted transmissions
retain an unknown end. Aggregate metrics, automatic retention, audio recordings, external
notifications and a durable editable future running order remain separate work.


### Upcoming audio consolidated into station Overview

Removed the separate Upcoming tab and moved its navigation to Overview's Up next
section. Compact current/next rows show source-proxied album images, recording metadata
and known durations; only the actual next runtime selection appears. Existing Upcoming
bookmarks redirect to the overview anchor. Live overview polling now reconciles changed
nodes in place, retaining unchanged artwork and focused controls.


### Overview navigation correction

Removed redundant incident/history/up-next shortcuts and the zero-incident row.
Operational notices appear only for active incidents or recording-health problems.
Live overview contents explicitly target the main workspace for navigation; the
outer polling container retains its own update target. This prevents nested pages
when following presenter or future content links inside the live region.


### Producer history table and quiet updates

Replaced technical event cards with a compact named broadcast-history table and producer
CSV. Removed visible IDs, source paths, emitted bytes and generic selection messages;
retained speech, credits and useful failure explanations. In-progress broadcasts no longer
show a changing checkpoint duration or unknown end. New snapshots capture the playlist
name; legacy rows can show a labelled current name without altering stored history.

Fixed false refresh notices caused by byte checkpoints, live preparation state and
unrelated events outside active filters. Initial and polled versions now use the same
filtered/page-scoped semantic history; completion remains a meaningful update. Repeated
weekly editions of a named show share one dropdown option and one cross-day filter.


### Counted history pagination and results card

Placed history results, CSV export and one shared pagination toolbar inside a card.
Added matching range/total, first/previous/next/last, page jump and 25/50/100 page sizes,
with preserved filters and out-of-range clamping. Count and page rows share one read
transaction; newest-record anchors keep older pages stable while new broadcasts arrive.
Refresh checks follow the selected page and count, and exports retain the full semantic
filter while ignoring page bounds. Regression tests cover filters, size changes, page
clamping, equal-timestamp ordering and new-arrival stability.


### All live screens use event subscriptions (17 October 2026)

Supersedes the polling descriptions in earlier milestone entries. Overview, Audience,
Assistant messages/activity, incident menu state and operational fingerprints now use
SSE subscriptions. Committed operations/audience/chat writes notify observers, including
background writes; runtime activity and recording failure have explicit notifications.
Heartbeats only keep the transport alive. Actual expiry/show/day boundaries use one-shot
deadlines. Browser reconnect/navigation preserves region ownership and snapshots;
Timeline and Incidents retain explicit result refresh. Removed the global Working banner.
Contributor rules, current architecture/API/UX guidance and a polling regression guard
are recorded in [live-updates.md](live-updates.md). Full checks, race tests and builds pass;
browser checks cover cross-tab review notifications, explicit refresh, automatic reconnect
and phone/tablet layouts.
