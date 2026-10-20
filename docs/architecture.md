# Architecture

## Implemented dependency direction

```text
cmd/stationos (composition, lifecycle, CLI)
  ├─ web → station/source/playlist application services + repository ports + catalogue sync
  ├─ persistence → station, sources, content, catalogue, playlists + embedded migrations
  └─ sources/upnp → sources + content
                       ↑
           catalogue → sources + content
```

The core packages do not import HTTP handlers, SQLite, UPnP XML types or each other in cycles. SQLite repositories are separate adapters sharing one explicitly passed database. There are no package-level mutable registries or services. SQL lives only in persistence and migration files. HTMX and templates are embedded; the UI works with ordinary forms as well.

## Domain boundaries and contracts

**Station** owns identity, description, timezone and locale. Its repository is a station-owned port; its service creates stable random identifiers and validates updates. Station owns its stable editable stream address; provider and runtime state belong to separate services. Policy and branding will expand this model through migrations.

**Content** describes all broadcast-capable audio, with an explicit kind and original/generated/derived origin. Duration is optional, resources preserve MIME alternatives, and source/object identity is stable. Unknown fields remain unknown. The initial UPnP adapter distinguishes music tracks from generic recorded audio rather than labelling every audio item music. It does not invent BPM, cue points or licence claims.

**Sources** owns shared configuration (independent of stations), `AudioSource` (Browse/Get/Resolve/Profile), optional Searcher, capabilities and restrictions. Browse returns StationOS entries, not DIDL structures. Metadata and artwork are included in AudioItem. Enumeration, search and recommendations are advertised unsupported by this adapter. Seeking is not assumed from a URL. Public broadcast and technical manipulation require operator responsibility for local content. Future commercial adapters must explicitly express restrictions and reporting requirements; enforcement must accompany any future audio execution path.

**UPnP adapter** lazily fetches the configured description; supports embedded ContentDirectory services, URLBase and relative control/resource URLs; escapes object IDs; checks SOAP counts; preserves unsupported objects in pagination; parses DIDL into domain items. Every XML response is limited to 4 MiB; calls have ten-second deadlines. Only configured-server DNS-pinned HTTP(S) addresses are reachable, including server-hosted high ports; loopback/link-local and cross-server redirects are rejected. OpenStream freshly resolves audio and opens it through the pinned transport. Its supported demuxer is explicit; manifests and cross-server targets are rejected. Conversion remains in the broadcast adapter. SOAP browse requests use fresh HTTP connections because some UPnP servers close responses without advertising connection closure.

**Catalogue** is a shared local derived index separate from sources and accessible to every station. Sync asks adapters for a complete catalogue enumeration root when available. MinimServer uses its verified unfiltered `0$items` view rather than recursively expanding virtual facet menus; verified Gerbera servers use their discovered built-in `Audio / All Audio` view to avoid importing separate virtual copies of each recording; missing complete views fail explicitly. Generic UPnP sources retain bounded root/container traversal. Sync traverses the chosen root/container pages, handles cycles/repeated objects, deduplicates identities, bounds time/pages/containers/items and total metadata bytes (64 MiB per import by default) and verifies per-container UpdateID/count consistency. It normalises identity and validates content before writing. A complete snapshot is atomically replaced only if the source revision is still current. A failed fetch or database transaction preserves old items. Successful replacement removes source orphans. Station/source records are never part of clear/rebuild operations. Explicit source removal deletes the connection and cascades its derived catalogue only; saved playlist rules and manual snapshots remain, with missing recordings unavailable. A concurrent import cannot recreate a removed source. Snapshot stability across different containers and incremental change tracking remain future work.

**Playlists** owns station-specific identity, description, dynamic genre/album rules, individual inclusions/exclusions. Rules match alternatives within a category and intersect selected categories against the shared catalogue; exclusions win and overlaps are deduplicated. Album identity is currently source plus exact album title, so same-title editions in a source are grouped. Genre identity is one exact trimmed comma- or semicolon-separated component of the imported values. New rule matches join automatically on the next catalogue import. Manual selections save content snapshots without foreign keys to disposable catalogue rows, so missing items remain visibly unavailable. A playlist is an unordered eligibility pool for human/AI curation. Sorting by title/ID supplies stable browsing pages and implies no broadcast priority. Resolution and mutations are transactional and bounded (100 rules, 10,000 entries). Playlist definition is separate from a future timed programme log or playback engine.

**DJs** owns reusable fictional presenter profiles (name, selected voice, editorial prompt and voice-direction vibe) independently of stations. Credential management belongs to shared AI settings in the producer domain. Profile metadata reads derive only a shared-key-present flag; DJ saves cannot set or clear credentials. The encrypted shared key uses AES-256-GCM with independent associated data and the database's existing 0600 `.secrets-key` sidecar. Back up the database and sidecar together. Missing sidecars with encrypted credentials fail startup.

**Persistence** uses modernc SQLite, one connection per process, WAL, foreign keys and a five-second busy timeout. This intentionally serialises database work for the initial deployment. Count and search results use one read transaction. Versioned SQL migrations run transactionally, verify applied checksums and reject unsupported newer schemas. Migrations are immutable once released. Frozen versions 1 through 10 fixtures verify upgrades to version 11, preserved identities/metadata, checksum validation and foreign-key integrity. Migration 2 removes source station ownership; migration 3 introduces authoritative station playlists; migration 4 removes saved ordering while preserving playlist membership. Migration 7 adds the selected voice, defaulting existing DJ profiles to Cedar without changing encrypted credentials. Migration 6 adds shared DJ profiles and encrypted credentials. Migration 5 introduces station broadcast assignment and unique editable stream slugs. Its immutable Go backfill runs inside the migration transaction and derives addresses from existing station names. The integrity command checks SQLite storage and foreign keys.

**Web** centres stations: homepage cards lead to per-station Overview/Playlists/Settings. Sources are shared; music browsing opens in playlist context. HTMX replaces the main content inside a stable shell, preserves titles/history, returns HX-Location on writes and falls back to complete ordinary HTML documents. Catalogue filtering/sorting and numbered pagination use ordinary GET URLs; selections use protected forms. Overview and cards subscribe to actual listener/current/next changes via SSE. See live-updates.md for the subscription contract. The player remains outside the main/history swap region. Batch genre/album checkbox selections commit atomically. UI conventions are recorded in user-experience.md. Writes call services and use bounded forms and CSRF tokens. Metadata is HTML-escaped. Authentication is enabled only when `STATIONOS_ADMIN_PASSWORD` is non-empty, regardless of the listen address. An unset or empty password disables login for management routes; CSRF tokens and origin checks still protect writes. The API has one versioned status contract; it does not expose SQL row schemas.

Page view construction is query-free for shared shell fields. Handlers explicitly load the data their screen uses: catalogue facets for track browsing and playlist selections, source counts for source overviews, and station cards for the producer overview and its live updates. Station workspaces scope playlist lists to their station; settings and creation forms do not depend on unrelated catalogue reads. Keep these dependencies explicit when adding pages or fragments.

Both shells use content-versioned stylesheet and script URLs so new builds load matching assets rather than stale cached controls.

Web presentation models live in `internal/web/view_models.go`: a small `pageView` carries shell/navigation context, while each feature owns its screen data and shared catalogue/playlist choice components. Handlers choose named page and fragment templates; `templates/shell.html` owns the persistent producer shell, and feature templates own headings, actions and bodies. Template selection never comes from request parameters. Keep template HTML formatted for reading rather than compressing whole forms onto one line.

`internal/web/render.go` owns template execution. Pages, targeted fragments and validation responses render completely into memory before the response status or body is committed. The producer renderer composes escaped page-template output into its shell; only that completed server-rendered output is marked trusted HTML. A template failure produces a clean 500, including when the intended response was a validation error. Event streams use the same buffered execution helper for each event before writing its SSE frame. Full navigation and history restoration render complete documents; only ordinary HTMX requests may select a targeted fragment.

**Operations** use JSON slog messages, health/readiness endpoints, source/station IDs, elapsed import time and explicit import failure logs. Imports are synchronous with two-minute deadlines; there is no background worker, progress persistence or scheduler. Graceful shutdown first cancels station producers and long-lived streams, then drains HTTP requests within five seconds. Source latency is logged per import; fine-grained metrics and persistent source health are planned.

## Authoritative versus derived data

| Data | Ownership | Rebuild behaviour |
| --- | --- | --- |
| stations | Operator configuration | Preserved |
| sources and revisions | Operator configuration | Preserved |
| playlists, playlist_rules, playlist_selections | Operator choices per station | Preserved |
| djs | Shared operator presenter configuration | Preserved |
| station_broadcast | Assigned station playlist | Preserved |
| schema_migrations | Schema history | Never cleared |
| catalogue_items and indexes | Reproducible source metadata | Clearable and replaceable |

User annotations, policies and future programme logs must live outside this disposable index. Future logs must carry an immutable execution snapshot rather than relying on a current catalogue row or an expiring source URL.

## Next domains: deliberately not implemented

Programming consumes policy and catalogue candidates to create a deterministic **programme log**. A log event will own station/event IDs, planned time, content/source references, selection reason, immutable resource/provenance snapshot and transition/fallback requirements. Actual starts/ends/failures/replacements belong to a separate playout audit record.

Production owns TextGenerator/SpeechGenerator/AudioProducer boundaries and creates assets ahead of airtime with provenance. DJ profile configuration is implemented; presenter assignment and execution policy remain future work. Production does not belong in an HTTP handler or source adapter.

Playout executes explicit log data with an injected clock and audio execution boundary. AI/network enrichment must stay outside its timing path. Outputs consume the programme audio independently of scheduling (initially a local output, later Icecast/HLS/recording). Advertising must schedule campaign-linked auditable events. Information retrieval, editorial selection and production remain separate stages. Users/roles will be authoritative station-scoped records, not hard-coded into the Station model.

Do not claim the continuous linear radio milestone until real deterministic scheduling, execution, current/next status and a playable output have been implemented and tested.

## Listener-driven broadcast milestone

`broadcast.Manager` owns station admission, runtime lifecycle, temporary random selection, real-time 128 kbps pacing and bounded fan-out. `broadcast.Repository` supplies assignments and an atomic, fully resolved eligible pool (up to 10,000 entries). Persistence validates station ownership before assignment; deletion clears assignment through foreign keys. Pools are refreshed at track boundaries and an already-selected next item is revalidated. Runtime state is in memory and process-local; multiple StationOS replicas must not independently serve the same station.

`broadcast.Encoder` isolates byte execution from selection. The initial FFmpeg adapter uses sources.StreamOpener, checks Streaming/PublicBroadcast permission and rejects sources requiring unimplemented playback reporting. FFmpeg receives only a forced music demuxer and pipe bytes, with pipe-only protocols, no inherited environment, one encoder thread, clean 128 kbps MP3 output, bounded startup/process waits and context cleanup. The manager paces bytes centrally because FFmpeg input read-rate alone allows startup bursts. No audio files are persisted. The one-thread encoder is shared by all station listeners; per-listener work is queue consumption and HTTP writing only.

A station starts on the first subscription and stops after its last listener. Concurrent first listeners share one worker; reconnect waits for cancellation cleanup before creating a successor. Capacity limits bound active workers and listeners. Fan-out uses immutable 4 KiB chunks, 32 queue slots by default, and evicts slow listeners without blocking peers. Closing a subscription signals handlers immediately rather than draining stale queued audio. HTTP writes have rolling ten-second deadlines that override the normal finite-response WriteTimeout. GET startup waits for actual bytes; HEAD cannot start playback. Stream routes accept a stored station slug, never an input URL. Management uses the existing authentication/CSRF policy; external streams deliberately allow unauthenticated GET/HEAD.

This implements playable shared radio with current/next delivery state, but not deterministic programme logs, AI curation, production, crossfades, gapless transitions, recordings or distributed output. The temporary selector is separate from playlist membership and can be replaced with a curator boundary. Broadcast remains listener-driven rather than running a schedule through zero listeners.

## Scheduling and production adapters

`internal/schedule` owns recurring station-local shows, overlap rules and occurrence resolution. Its persistence adapter joins configured DJs/playlists without exposing credentials. `internal/production` defines preparation, credentials, cached assets and audio-normalization ports; `internal/production/openai` implements bounded Responses/speech calls and receives decrypted keys only server-side. Cached assets are not catalogue tracks or playlist membership. `internal/broadcast` chooses eligible prepared candidates, fits durations near show end, resolves changes at track boundaries, and sends ready music/presenter bytes through one paced station fan-out. AI workers use bounded queues, contexts and preparation caches and cannot block music selection. Live execution is transient; a complete persisted programme/execution log remains pending.

## Producer card artwork

The optional source `ArtworkReader` retrieves image URLs from known current/next metadata using the adapter’s checked transport. The web edge exposes authenticated station/item artwork URLs and never accepts an arbitrary URL from a request. Raster format, size, timeout and concurrency bounds apply; private caching avoids repeated transfers during card updates. Card presentation combines configured default assignments with immutable runtime programme/current/next snapshots and does not infer on-air state from a schedule clock.

## Prepared audio transitions

Per-track DJ links are generated while the current track plays. Ready speech is emitted through the same paced fan-out at the music boundary; cancelled speech workers and source/process cleanup are joined only when the station stops, rather than stalling the next segment. Each station admits one speech-generation worker, including cancellation cleanup, so slow cancellation cannot accumulate AI requests.

The optional `broadcast.PreloadingEncoder` primes the queued song in parallel with current playback. FFmpeg retains one 4 KiB output chunk and relies on bounded pipe backpressure for further decoding. One current and one queued encoder are shared by every listener, rather than multiplying work per connection. The queued item and show are revalidated before use; changed or unready preparations are cancelled. Last-listener cancellation releases both encoders and joins their workers. Source failures and late AI still use the existing music/retry path.

Presenter normalization removes only leading/trailing near-silence, preserving pauses within speech. The presenter cache key versions this audio contract, so old padded introductions/outros are regenerated. MP3 format, bitrate, sample rate, fan-out pacing and listener connections remain consistent across music and chatter. Decoder/process errors are reported during asynchronous cleanup without inserting a process-wait delay into the stream.

## Recording credit roles

UPnP artist/author entries are multi-valued, role-bearing credits. Preserve track performers in `AudioItem.Artist`, with separate Composer, AlbumArtist, Conductor and ambiguous Creator fields in catalogue JSON. Composer-role entries never overwrite performers, and an ambiguous creator is not relabelled as a performing artist. Multiple credited performers/composers are deduplicated without dropping their names. No schema migration is needed: source refresh rebuilds derived metadata and playlist resolution uses the current catalogue entries.

Both DJ curation candidates and current/next chatter inputs use explicit Performers and Composer labels, alongside album-level and conductor credits. Instructions distinguish who performed a recording from who wrote the work, allow optional composer commentary and prohibit invented missing credits. Presenter cache keys version the credit contract so cached speech cannot retain the previous attribution.


## Event-driven station overview

Broadcast runtime observers use bounded, coalesced notifications for displayed state/listener changes; ordinary audio frames do not notify when state is unchanged. Successful management writes announce committed configuration changes. Observers are independent of audio subscriptions and cannot start a station or change listener counts.

The authenticated `/producer/stations/events` endpoint retains one connection per live homepage, bounds concurrency to 64, resets write deadlines for each flushed message, and releases observers on disconnect. Server-rendered station HTML is hashed so unchanged content emits no data event. A quiet 15-second heartbeat keeps the transport open without re-reading state. Clock-dependent public schedule changes use one-shot boundary timers. The browser patches only differing nodes while retaining card identity, unchanged artwork and focused controls; navigation closes the connection and history return opens a fresh one. Station workspaces, Audience, Assistant and operational fingerprints also use subscriptions; see live-updates.md.


### Persistent player metadata

The shell player shows the actual current and next titles and performing artists, with DJ on air during speech. A station-specific authenticated event connection updates only changed metadata and remains active across HTMX navigation. It does not start audio or count as a listener; switching stations replaces the connection and Stop closes it. Empty runtime states show Nothing playing and Not queued. The layout wraps for smaller screens.


## Bounded streaming reserve

The serve command configures a five-second `StreamBuffer`. A delivery clock permits a bounded startup/catch-up burst and then real-time 16,000-byte/s MP3 output. Supply stalls no longer reset the clock after one second: short deficits are repaid, while recovery after a prolonged outage is capped at the configured lead. The station keeps a byte-bounded recent window (up to 80,000 bytes, at most 64 chunks). Listener admission snapshots that window into one initial queue entry under the same lock as subsequent fan-out, avoiding a hole or duplicated audio at the join boundary. The same audio feeds all clients; no listener starts a separate producer.

Each FFmpeg stream uses a cancellable 32-chunk read-ahead channel, normalising pipe reads into 4 KiB output blocks and preserving the final partial block/errors. A single in-flight read plus the channel holds at most about 8.5 seconds of encoded bytes per reader; the current and preloaded next item remain bounded separately. Backpressure bounds further source consumption. MP3 format and bitrate stay unchanged; no fake keepalive bytes are inserted into audio. Starved supply reads over one second and slow-listener eviction are logged. Physical radio and deployment-network acceptance remains necessary; outages beyond the reserve cannot be guaranteed seamless.

Schedule resolution and remaining-time planning use the audio timeline at track boundaries, accounting for bytes sent ahead of wall-clock playback. A startup burst cannot add an extra outgoing track just because its delivery ends before its audible show boundary.


## Station mixer

`internal/mixer` owns separate station music and DJ amplitude percentages (0–400%, default 100%). Migration 10 adds authoritative station mixer configuration with deletion cascading; an absent row retains unity gain. Both levels save atomically, independently of station details and playlist assignment. Mixer controls commit through protected JSON autosave requests without navigation or form swaps; the browser serializes writes and retains the latest pending levels, with inline retry on failure.

Music loudness normalisation, gain and peak protection run in the source FFmpeg encoder. The station Mixer has an Automatic music loudness switch, off by default for existing and new stations (migration 36). When enabled, music uses single-pass loudnorm at -16 LUFS, -2 dBTP and LRA 20 before station gain, bringing differently mastered recordings closer in perceived level with a wider dynamic range than speech. This is live dynamic processing, not a whole-file ReplayGain scan: some musical dynamics can change and CPU use increases. It adds bounded filter lookahead rather than requiring a complete source download. Tail detection stays before normalisation, so its silence threshold is unchanged. The explicit final 44.1 kHz rate downsamples true-peak analysis before the existing MP3 encoder. The broadcast manager reads levels at track boundaries and rejects preloaded audio prepared with an older music level. Changes do not interrupt the current song or reconnect listeners. Prepared speech is gently compressed and levelled to -14 LUFS/-2 dBTP before caching (audio contract version 4), following Retro Radio's `agentSpeechFilter`. DJ gain is applied to cached speech locally at airtime, after levelling and before MP3 encoding, without changing shared assets or making new AI calls. Clamping protects amplified peaks after the station fader; music normalisation precedes that fader so it does not reverse the adjustment. StationOS retains sequential music/speech segments and its existing bounded fan-out.

## Immediate music-to-presenter handover

Following Retro Radio's bounded reservoir approach, shared read-ahead and one continuous delivery clock cover source jitter. Music now passes through a checked FFmpeg PCM decoder, a bounded near-silence tail reader, then the existing fixed-format MP3 encoder. The tail reader retains at most five seconds of stereo 44.1 kHz PCM (882,000 bytes); it flushes pauses unchanged if audible music resumes and drops only the final retained near-silence at EOF. Detection happens before mixer gain, so muting a track cannot truncate it. Audible fades are retained above the conservative -60 dB sample threshold. Each current/queued song uses a decoder and encoder; songs are never buffered in full.

Per-track links and cached show introductions/outros prepare configured DJ gain in the background, before becoming ready for playout. Raw cached speech stays immutable. Unchanged gain plays directly from prepared bytes; a mixer change after preparation is applied from original audio at the next announcement. Late generation still skips the link and continues music. Synthetic FLAC-to-MP3-to-speech tests verify final silence removal, preserved internal pauses and less than 150ms of codec padding at the handover. This is sequential speech immediately after music, without overlapping voices with songs.


## Fictional DJ background contract

Migration 12 adds background and portrait_url to operator-owned DJ configuration. It enriches only the original named starter identities and leaves encrypted credentials untouched; the frozen version 11 fixture verifies historical upgrades from conversation-speed configuration. Schedule snapshots carry both fields. The OpenAI adapter sends background as labelled fictional character data for curation, show introductions/outros and between-track links. Explicit instructions permit occasional supplied first-person reflections and listening observations, while prohibiting invented real-world encounters, recording facts and unsupplied biography. Existing 45-word limits apply. Portraits are never sent to the text or speech APIs and are excluded from prepared-audio cache identity; background changes invalidate prepared speech. Bundled JPEGs are embedded, and validated HTTPS portrait URLs load directly in the browser under the image CSP without sending a referrer. Configuration changes never invoke AI.

## Admin-managed DJ photos

Migration 13 adds a bounded `dj_photos` blob table, referencing DJ identity with cascading deletion. The web adapter accepts authenticated, CSRF-protected multipart profile forms, bounds request/file sizes and image dimensions, decodes JPEG/PNG, flattens transparency, strips metadata and scales to at most 768 pixels. The domain carries a write-only photo change; persistence saves profile and image atomically; credentials are managed separately in shared settings. Versioned same-origin photo URLs are generated internally, work with existing scheduled DJ snapshots and are served through authenticated management routes. No directory mounts, filesystem paths or remote URL entry are needed by the admin. Existing supplied portraits are preserved until replaced or removed. Photos are part of normal SQLite backups.


## External radio metadata

External MP3 clients requesting `Icy-MetaData: 1` receive `icy-metaint: 8192` and standard ICY `StreamTitle` blocks: performer and track title during music, presenter name and show title during speech. Station identity and 128 kbps bitrate are advertised in `icy-name` and `icy-br`. Clients without that opt-in retain plain MP3 bytes; HEAD advertises headers without starting production.

Immutable stream frames retain segment metadata alongside shared audio, including the bounded recent window and listener queues. Metadata follows the bytes delivered to each listener rather than a potentially newer producer snapshot. The HTTP adapter inserts length-prefixed, UTF-8-safe, bounded and padded metadata every 8192 audio bytes; unchanged titles use empty blocks. The station's encoder, pacing and listener lifecycle remain shared. A physical radio check behind the deployment proxy is required to confirm device display behaviour.

## Station logos

Migration 14 adds station-owned bounded JPEG blobs with content versions and cascading deletion. Station configuration and logo changes save in one transaction; database backups include uploads. The HTTP edge shares the DJ photo decoder, limits and normalization. Only GET/HEAD `/station-logos/{id}.jpg` is public for device artwork; settings and multipart writes retain authentication and CSRF protection. Missing custom logos serve the bundled JPEG, and nonexistent stations return 404. URLs use the configured public origin when advertised in the `icy-logo` header and ICY `StreamUrl` field. Clients without ICY opt-in still receive plain audio. Artwork discovery varies by radio firmware and directory, so physical-device acceptance is required; no universal device display guarantee is implied.


## Station track rotation

Migration 15 adds station-owned `repeat_minutes` (default 180, range 1–10080) and `station_track_plays`, one latest actual start per station/exact content ID. History survives listener dormancy, process restarts, show/playlist changes and catalogue rebuilds; station deletion cascades it. No duplicate recording detection is performed: operators exclude duplicate copies from playlists.

The broadcast history port reads policy and recent plays atomically at track boundaries, along with the latest track ID for consecutive-repeat avoidance across reconnects. Candidates are filtered before curation, random selection and duration fitting. Current music is provisionally reserved for next-track predictions; queued/preloaded items and cached DJ choices are revalidated against actual history before airtime. Start-to-start intervals use the programme timeline. Only the first music bytes delivered to listeners count as a play, including tracks interrupted afterwards; preparing audio, speech and zero-byte failures do not. Failed history reads/writes stop production rather than broadcasting with an unenforced policy.

When every alternative is too recent, only the least recently played alternatives enter selection. Consecutive repeats are avoided whenever the pool contains another track. A single-track pool continues with an explicit relaxation. Current fallback state is exposed on station cards and Overview and clears with subsequent segments. SQLite reads only history within the configured interval plus the last track identity; storage retains the latest timestamp per station/track, allowing a longer interval to be applied later.


## Public station publishing

Migration 16 adds station-owned visibility (`listed`, `unlisted`, `private`), validated in the domain and saved atomically with identity/logo settings. Its Unlisted default preserves existing direct streams without publishing stations in a directory; frozen version 15 upgrade coverage verifies preservation. The same stable stored stream slug identifies `/radio/<slug>` and `/streams/<slug>` independently of station names.

The HTTP edge separates public listening routes from management at `/producer`. Only GET/HEAD listener pages, events, scoped images and static assets bypass configured management authentication; all management writes retain bounded inputs, origin checks and CSRF. Protected legacy management URLs redirect preserving query strings. Existing uploaded `/djs/<id>/photo` references remain authenticated for compatibility. Public presenter-photo proxies require that the presenter belongs to the station schedule, and public track-artwork proxies expose only current/next items through the existing checked source transport. Private page/media access returns 404; private audio preview uses protected `/producer/streams/<slug>`. An explicit transition to Private stops the broadcaster, releasing listeners.

Public templates project listener-visible identity, tracks and programme labels. They do not render credentials, editorial prompts, source addresses, playlist management links or raw producer errors. Public SSE observers share the bounded 64-view admission limit, use change notifications and 15-second reconciliation, hash rendered content and enforce rolling write deadlines. They never subscribe to audio. Publication is rechecked on updates and a revoked station ends public event connections. Public page and player observers patch changed content while audio remains outside the HTMX history region. Runtime state remains process-local and listener-driven.

### Audience participation

Scheduled shows opt into track requests and shout-outs independently. Public
submissions belong to the next advertised occurrence of an enabled show, including
its schedule revision. Changing the show expires pending and approved messages;
submissions never carry into another week's programme. The default is disabled.

`audience.Service` resolves public station policy, occurrences and eligible tracks.
The SQLite adapter commits submissions with a second policy/revision check, limits
three submissions per anonymous cookie in ten minutes, caps each station at 200
active items and removes records older than 30 days during queue activity. Public
forms use a dedicated HttpOnly, SameSite cookie and HMAC form token; producer
credentials and form tokens are never exposed. A bounded in-memory address limit
also controls cookie-reset traffic, using hashed socket addresses rather than
forwarded headers. Configure the public HTTPS origin behind the proxy.

The existing bounded DJ link worker reviews up to four pending items during a live
show, using the Decisions API (`gpt-6-luna`) over projected, untrusted listener data.
Named predicate answers route clearly suitable submissions to approval (probability
≥ 0.95) and clearly unsuitable ones to dismissal (≤ 0.10). Intermediate estimates
and refusals enter a separate producer-review state so they cannot block later
submissions or repeatedly incur AI screening calls. These initial thresholds
need evaluation against labelled station submissions; they do not establish legal
status. Screening has a 15-second timeout. A failed/incomplete response leaves items
pending. Producer decisions win over
in-flight AI review. No listener means no production or review. AI credentials are
the shared key in Assistant settings; without that key, shows cannot generate spoken request introductions or
shout-outs. Approved requests wait for a presenter that can introduce them.

Approved requests are considered only from the revalidated rotation pool with a
known duration fitting the remaining programme time. They do not modify playlist
membership. Spoken links offer up to three approved shout-outs, or one matching request dedication, and
validate the model's used IDs. Prepared speech is rechecked immediately before
playout. Requests are marked used after the first delivered music bytes; shout-outs
after the first delivered speech bytes. Generation and cancelled preparation never
consume a submission. A request dedication does not consume the request before its
track plays. Failures continue normal music selection.

Request introductions bind speech to the exact selected catalogue ID. A matching
approved request takes precedence over general shout-outs, even without a
message. Its requester, dedication and authoritative queued recording are supplied
as untrusted data in the DJ speech prompt. The speech adapter requires an explicit
acknowledgement of that request ID and rejects another track or an omitted request.
The broadcaster opens the recording before its introduction, rechecks approval and
plays that same reader after the link. Only a completed introduction followed by
actual music bytes fulfils the request; cold starts, omitted introductions and
producer dismissals do not consume it. Source failures retain the request.

Public contribution GETs project an exact enabled schedule occurrence. The
`contribute` route offers only its supported types; request/shout-out routes serve
the form and thank-you state. A `contribution-panel` HTMX target receives a scoped
fragment, while ordinary navigation and history restores receive the full station
page. Successful writes use a targeted HX-Location to a GET confirmation; errors
re-render the submitted occurrence and escaped input without changing its target.
Existing CSRF, origin, visibility and durable submission validation remain the
authority for every write.

Migration 18 adds an optional public show description with an empty default;
frozen version 17 upgrade coverage verifies that existing show identities and
participation settings survive. Public show pages are station-scoped and respect
visibility. Their contribution GETs and POSTs additionally verify that the
occurrence belongs to the show in the path.

The public daily programme projects schedule occurrences onto station-local civil
days using calendar arithmetic, including daylight-saving changes and overnight
shows. Day navigation is bounded to today plus six days. Unscheduled gaps appear
as continuous music, with no fabricated presenter. Participation links use the
displayed occurrence key and stop when it ends, avoiding accidental next-week
submissions. The selected day persists through contribution forms and redirects.

Public schedule rows use ordinary links with a cached HTMX
load of a station-scoped show fragment. Each show owns a separate contribution
target and track selector, so multiple open shows cannot replace each other's
forms. Expanded content remains mounted when collapsed. Contribution steps and
confirmation update that card without pushing URLs or scrolling the page; day
links replace only the programme region. Full-route fallbacks retain visibility,
CSRF, occurrence and show ownership validation.

Listener swaps use explicit `show:none`; confirmation HX-Location also disables
history pushes. Track searches return only their show-scoped selector. Revealed
show contents remain mounted and carry a loaded marker through HTMX
history restoration, preventing a restored form from being fetched over again.
Short height animations are scoped to the changed show region and disabled for
reduced motion.

Listener track requests search as the user types after a short pause, updating only the show-scoped results. Show up to eight eligible recordings with title, artist and album; provide initial-search and no-match messages. Native radio choices support keyboard and ordinary form submission. Enhanced forms keep the chosen recording in a persistent summary across searches, preserve listener input, and ignore obsolete search responses.

Bare MP3 URLs can receive a tiny `Range: bytes=0-1` media probe before the playback GET. Such probes retain the compatible live-point 200/audio-mpeg response, deliver one initial audio chunk and finish, releasing their subscription. Ordinary/open-ended stream GETs remain continuous and independent; app probes carrying an opaque listener ID retain session replacement. Never merge external listeners by IP address, user agent or shared browser cookies. Verify direct Safari playback after deployment.

The producer audience queue explains that review runs during spoken-link preparation. Its results subscribe to committed contribution/review/use changes and actual expiry deadlines, preserving filters, pagination, scroll and focused actions. Tick/X icon actions retain accessible names and tooltips. No-listener and missing-shared-key notices reflect measured runtime state. AI review failures are logged without listener message text; failed reviews remain pending.

The oldest offered approved shout-out must be acknowledged by the speech adapter, as requests already are; its omission fails preparation and retains approval for a later link. Transport diagnostics identify the OpenAI endpoint, elapsed time and safe failure category without logging raw errors, credentials or listener text. Context cancellation remains identifiable so ordinary playback-boundary cleanup does not generate failure warnings.

Shout-outs are explicitly framed as listener contributions in the DJ prompt. A link offers the three oldest approved shout-outs and requires the oldest; the DJ may include the others in one brief section (up to 75 words for a batch, 45 otherwise). Only unique, offered IDs actually acknowledged in the speech receive playout receipts; omitted messages remain approved. Matching track requests retain their separate required introduction and priority.

DJ production retries temporary OpenAI transport failures and HTTP 408/429/5xx
responses at most twice, waiting one then two seconds within the existing caller
deadline. Authentication, request validation and incomplete model output are not
blindly retried. Retrying speech reuses the generated text; it does not regenerate
the DJ script. Cancellation at playback boundaries stops retry waits. Music never
waits for these workers. Presenter status follows the latest link preparation
result, so an earlier failed show plan cannot mask a subsequently ready link.
Incomplete response logs use known reason classifications without model content.

## Internal producer assistant

`internal/producer` owns the conversational runner and a transport-independent
capability registry. Each capability has a name, description, JSON Schema input,
mutation flag and typed dispatcher returning structured data, an outcome and an
optional producer-page link. The registry calls existing application services and
repository ports; it does not call HTTP handlers or execute SQL. The registry remains authoritative for model-proposed actions. No MCP server or external tool-execution endpoint is registered.

`internal/producer/openai` adapts the Responses API. Returned provider items,
including reasoning, are carried forward within each batch. At batch boundaries,
completed calls become ordinary text/results; unanswered calls are never replayed.
Requests use `store: false`, fixed endpoints, bounded responses, blocked redirects
and a one-minute HTTP timeout. Four conversations may run concurrently, with one
run per conversation and up to eight calls per model response. There is no fixed
24-round or five-minute request cutoff: every 24 rounds the runner compacts context
and continues the same task. Twelve consecutive rounds without new successful
results or changed task progress pause the run. The producer can stop it explicitly.
Tools retain their own deadlines and bounds; whole requests are not one transaction.

Migration 19 stores settings, conversations, message/action history and submitted
turn identifiers. Migration 20 adds durable task progress. The runner's private
`save_task_progress` tool records the brief, completed steps, actionable remaining
steps and blockers. Progress and completed call results are checkpointed after
each action. Every human request is retrieved independently of the UI's last 100
messages, preserving original schedule details even in legacy failed conversations.
Compaction keeps human instructions plus recent results, with the task checklist
supplied independently. A failed response preserves these checkpoints. Startup
marks interrupted runs failed without clearing context; Continue saved request
resumes from the brief, checklist and recorded outcomes after checking current state.
Successful creations are deduplicated within a run, including across batch changes.
A crash between a write and its outcome record can still leave an uncertain Started
action: resume must inspect current configuration before repeating it.

Migration 21 originally centralized AI configuration. Its encrypted key in
`producer_settings` serves producer chat, DJ curation/writing, speech and audience
review. Producer, default DJ, speech and audience models are independently configured;
migration 24 makes DJ curation/writing model selection exclusively global. Model-only edits preserve the key.
Speech model changes alter prepared-audio cache keys. DJ rows never receive new
credentials. Existing shared credentials take precedence and remove old DJ copies.
If no shared key exists and all legacy DJ keys have one distinct value, startup
re-encrypts that value into shared settings and clears the copies. Ambiguous legacy
keys remain inactive until a producer chooses the shared key; saving it clears them.
The sidecar cannot be silently replaced while any encrypted credential exists.

All assistant routes remain under `/producer` with existing optional authentication,
origin validation and CSRF checks. Producer authority applies to requested changes
across stations; station context is a default rather than an access restriction.
There is no extra approval queue or MCP endpoint. Source metadata, DJ prompts and
listener text remain untrusted data. Keys are managed only in Assistant settings
and are absent from tool schemas and model inputs. New DJs immediately use the
shared credential; they never require individual key configuration. Missing shared
credentials do not block ordinary station, playlist or schedule configuration.

The UI displays the latest 30 conversations in the current context and the last
100 messages, with older conversations retained at their URLs. Event subscriptions update only
messages/progress and preserves compose controls, draft text, focus and scroll.
Interrupted runs offer Continue saved request; active runs offer Stop assistant.

Producer model HTTP 429 errors inspect bounded code/type fields and never display
raw provider error bodies. Billing/quota errors pause without retry. Only explicit
temporary rate rejections retry the same unexecuted model request, at most three
times, with cancellable backoff and provider Retry-After. Delays over 30 seconds
pause rather than retry early. Transport failures remain non-retryable. Successful
Responses usage is shown live and recorded at the end of a run; counts include
cached input and do not imply a monetary cost. Rejected requests with no usage
report cannot be included in those measured counts.

Producer DJ selection assesses existing shared profiles before creating presenters.
DJ list summaries retain bounded background/voice direction, with get_dj providing
full profiles. The shared tool registry serializes DJ saves and rejects colliding
names (case/spacing/dash variants and known station suffixes), including renames.
Existing legacy profiles remain editable without renaming; name collisions never
silently update or replace a profile. Suitability remains an editorial model
judgment; new presenters must use original names when existing DJs do not fit.

Producer playlist selection inspects the requested station’s existing rules and
resolved tracks before creating another pool. Suitable pools are reused by ID
without changing their definitions. Assistant playlist saves serialize name checks
and reject same-station collisions, including case/spacing and station-suffix
variants. Names may repeat across stations; cross-station playlist assignments
remain invalid. Suitability depends on actual eligibility and availability and
remains the assistant’s editorial judgment.

Failed live show preparation is retried at the next song boundary, without an
application retry timer. Boundary admission is non-blocking: music continues
while preparation runs. Pending or successful show speech preparations are not requested again.
Success clears the failure state, with the ready speech used at a subsequent music
boundary. Upcoming DJ preparation also retries through boundary prefetching;
expired shows are not retried. Requests remain listener-driven and stop with the
broadcast. This permits recovery when API credits or availability change after
the initial attempt; external funding is not directly observable by the app.

The producer registry exposes generate_station_logo and generate_dj_photo through
an injected image-generation port. Station/DJ creation generates one artwork asset
automatically; the runner records/checkpoints the entity ID before image work.
Failed images return artwork_error alongside the committed creation, never an
ambiguous creation failure. Standalone artwork tools preserve current assets unless
replacement is requested and re-read metadata before saving. Raster validation,
metadata removal and JPEG resizing happen before existing logo/photo persistence.
One image job runs at a time. The OpenAI image adapter uses the shared encrypted
key and independent image model, a four-minute deadline, one low-quality square
JPEG per request, bounded base64 responses and no automatic billable retries.
Image usage is included when reported; token counts do not estimate image cost.
No image bytes or arbitrary image-fetch URLs enter the assistant tool transcript.


## Provider connections and portable text execution

Migration 23 adds operator-owned `ai_connections` and role routing, without
changing released migrations or existing OpenAI settings. The frozen version 22
fixture verifies the upgrade. Existing connections, keys and model selections
continue to use OpenAI. `internal/ai` defines configuration and text ports;
`internal/inference` implements routing and native Ollama `/api/chat`, Anthropic
Messages and Gemini generateContent, plus generic OpenAI-compatible chat
completions. Services implementing a different protocol need an adapter, not a
base-URL substitution. Any compatible service can be configured, but its selected
model must actually support tools and structured output. Provider-hosted agents,
remote tool execution and external MCP endpoints are not exposed.

Assistant, DJ curation/writing, audience review, speech and artwork each choose a
connection and model. Speech/artwork currently use OpenAI connections. Every DJ
uses the global curation/writing provider and model. Migration 24 removes the
obsolete DJ model column without altering profiles, schedules, credentials or
global choices. DJ reads and scheduled snapshots project the global model and include an AI configuration revision, so provider
and model changes invalidate pending and prepared work at song boundaries. Connections hold one credential, encrypted with
AES-GCM and associated data bound to connection ID, using the existing sidecar.
The historical OpenAI credential stays in its original encrypted location and is
not copied into local connections. Metadata and readiness flags contain no secrets.
A missing sidecar fails startup if *any* connection holds an encrypted credential.

The text port separates show planning and link writing from speech synthesis.
Local next-track selection works without speech credentials;
music continues and a producer runtime notice explains missing speech. Native
Ollama uses function arguments as JSON objects, named tool results, a JSON schema
in `format`, and bounded `num_ctx`/`num_predict`. Compatible services use string
function arguments, tool-call identifiers and JSON-schema response format.
Anthropic uses tool-use/result blocks and a forced schema tool for structured
text. Gemini uses function calls/results and response JSON Schema. Gemini tool
signatures survive only the current live protocol exchange, not recovery.
Curation, listener acknowledgements and audience decisions remain validated by
StationOS. Local/private audience review uses explicit approve/dismiss/uncertain
JSON decisions, with exact-ID and full-batch validation. OpenAI retains Decisions.

One shared local/native inference gate admits one active request and eight queued
requests, across connections. Queued short live links and audience review have priority over show planning,
which takes priority over producer work; running work is not preempted. Capability checks
share that gate. Queue waiting is separate from generation: live work can wait up to the
connection response deadline for an admitted request to finish. Producer conversations
remain in the bounded queue until admitted, stopped or cancelled by server shutdown;
they have no separate 30-second admission expiry. Each admitted request then has
a configurable 15–600 second response deadline (180 seconds for private/native
connections; existing OpenAI requests retain their 60-second limit), cancellation, a 1 MiB response bound, and configured context/output
budgets (default 16384/2048, context range 2048–32768). Input token counts are
estimated at three UTF-8 bytes per token, including tools; essential requirements
are never trimmed to fit. For assistant conversations, completed tool results are reduced to exact outcome/identity references, while original human messages, the approved proposal, current summary and task instructions remain protected. This runs before dispatch against the endpoint budget, including tool schemas and reserved output. The configured output limit is a ceiling: after removing optional completed read results, the assistant reduces the per-request output allowance if needed to fit protected requirements, approved proposals, summaries and committed-action outcomes. It reserves a small framing margin when adapting, never changes the saved endpoint configuration, and still pauses if the essential minimum cannot fit alongside a 256-token reply.
Ollama responses are not rejected merely for exceeding context minus the maximum output allowance: that allowance is a ceiling. Assistant requests disable input truncation and context shifting in the Ollama API, and replies reporting a prompt at the full context boundary are discarded before tool execution. Typed full-window and HTTP 413 input failures retain the saved request and receive one bounded recovery attempt with compacted history and a small tool set.
Exact tokenizer-independent preflight counts are not available for every provider.
Next-track curation is separate from show introductions and handovers. Broadcasting filters station rotation and excludes the current track when alternatives exist, then supplies at most ten candidates. The shortlist favours least recently played recordings, varies ties using current-track identity, and narrows to known fitting durations near show end (or the normal duration planner's fallback when none fit). During the current track, one bounded worker asks the selector for one recording, validates its ID against that shortlist, then prepares the spoken link for that exact choice. Listener requests retain priority and bypass AI selection. The choice updates live next-track state independently of speech success. At playback boundaries, current playlist membership, rotation and show identity are revalidated; stale spoken introductions are suppressed. Late, refused, invalid or failed choices leave normal music playback running, and workers are cancelled when listening stops.

The production selector port returns one catalogue ID. The OpenAI adapter uses a Decisions `choice` question for `gpt-6-luna`, with the curation connection's credential independently of speech. Other models/providers use a strict single-field JSON schema enumerating the offered references. Both paths receive short request-local IDs, bounded recording metadata, current-track context, DJ editorial style and remaining show time; source URLs, artwork and credentials are excluded. A singleton shortlist needs no inference. No provider builds a show-wide track list, and no duplicate-list correction path remains. Show preparation caches only bounded introduction/sign-off speech, with a new cache contract version to avoid reusing old plans. Speech and curation readiness remain independent.
Music reads never acquire the inference gate. Existing pending/success deduplication and song-boundary retries apply to show speech preparation.

Durable checkpoints normalize completed results and assistant text. Recovery
removes provider-private reasoning and unfinished protocol calls, independently
supplies all original human requests and the saved checklist, and retains recent
Started/outcome records for uncertain mutations. Admitted assistant runs pin role
routing; resumed runs use newly saved selections. Provider failures never directly
replay a mutation. Registry validation and duplicate protections remain unchanged.

Each role uses only its selected endpoint and model. Provider failures remain failures; StationOS never switches to a cloud endpoint. Historical fallback fields in saved JSON are ignored and omitted from subsequent saves.

Connection endpoints are configured through protected producer settings, never
model tools or chat. Deliberately configured loopback/private HTTP(S) addresses
are supported; URL credentials, query strings and fragments are rejected. Every
redirect is blocked, including injected clients. Credentials use only their
connection and protocol header; errors never contain response bodies, raw URLs
or credentials. Discovery lists models without generation, download or substitution.
Reachability, listing membership and explicit tool/JSON tests are separate results.


Migration 25 adds `request_timeout_seconds` to provider connections. Production
budgets include queueing, the selected response deadline and a separate bounded
speech allowance; legacy direct OpenAI preparation retains its original deadline.
Music delivery never waits for these budgets. Logs distinguish queue expiry before
request dispatch from a slow/ambiguous provider response; timeouts never trigger
cloud fallback. For DJ planning, spoken-link writing and local audience review,
Ollama's bounded, cached `/api/show` metadata determines whether `think:false` is
supported. It is sent only when explicitly advertised; other thinking controls
are never guessed. Spoken-link/review output is capped at 512 tokens or the smaller
connection limit. Producer assistant thinking normally remains at the selected model's
default. After a confirmed output-limit interruption, recovery requests ask for
thinking to be disabled only if the model explicitly advertises support for
`false`; unsupported controls are omitted. Frozen version 23 and 24 fixtures verify both historical upgrades.


Live inference admission is sized against the same bounded connection deadline
as the active request, rather than expiring every queued link after 30 seconds.
The production budget includes this wait, a full generation deadline and speech.
Song-boundary cancellation removes stale queued links without dispatching them;
last-listener cancellation releases all outstanding work. Within the eight-ticket
queue, short spoken links and audience review are admitted before show preparation,
then producer conversations. Running calls are never preempted or automatically
replayed, and music never waits for the inference slot. Queue expiration logs include
the actual wait limit; a shorter parent deadline can still end an admission early.

Speech readiness and curation readiness are separate broadcast state fields. A ready spoken link remains playable when next-track curation fails; both statuses and AI notices participate in live update comparison.

The Assistant navigation separates endpoint management at `/producer/assistant/endpoints`, feature routing at `/producer/assistant/settings`, and chats at `/producer/assistant`. Historical settings connection URLs continue to render endpoint forms. Endpoint credential changes preserve all role routes and model IDs, using the existing encrypted configuration store and CSRF/origin protections.

Model discovery uses read-only provider listings with a ten-second deadline. The CSRF-protected endpoint save validates its configuration and model-list response before committing. Failed discovery cannot modify the saved endpoint or credential. OpenAI credential replacement also validates discovery before saving; explicit credential removal remains available. Saved endpoint pages and feature model fields fetch lists automatically. Feature model fields fetch suggestions from saved endpoints and keep manually entered selections. Discovery never invokes capability tests, inference, downloads, or model substitution.

### Large-library assistant exploration

`catalogue.SummaryService` queries a summary repository with bounded source, exact-genre and text filters. The SQLite adapter aggregates counts, known duration, metadata gaps and the top 1–20 genre/performer-credit values inside one read snapshot. It does not materialise individual recording metadata. `summarize_music` is the assistant’s first library-exploration tool; `search_music` defaults to five recordings and caps pages at 20. UI catalogue pagination remains unchanged.

After a successful measured summary, earlier detailed catalogue pages are removed from active and saved working context. Totals, filters, the summary, mutation outcomes and human text remain; fresh targeted pages stay until a subsequent summary. Failed summaries cannot discard earlier pages. Checkpoint compaction preserves chronological order and the approved assistant proposal so a short “Yes, please proceed” retains its meaning after failure/restart. Local inference compacts completed exchanges before dispatch; unanswered tool exchanges and the latest exchange remain protected. No transport retry or model/context-setting change is involved.

Assistant-created show pools use the programme name or musical identity and a description of their intended music, rather than day-only or generic time-slot labels. Recurring broadcasts reuse the same pool; distinct editorial requirements should be reflected in verified eligibility, not merely different names for identical genre rules. Suitable existing pools remain reusable across shows.

After successful show creation, editing or deletion, the producer runner reads the affected stations’ saved schedules before accepting a final answer and sends the measured readback for comparison with the original brief. Show tool results include Monday-based day names and formatted local times. The mandatory readback groups recurring editions with identical programme, presenter, music pool and local times, preserving each edition’s exact saved ID and weekday without repeating full row metadata. Different times or assignments never merge. This enforces readback; assessing natural-language requirements still depends on the model.

### UPnP source discovery

The source domain exposes a read-only Discoverer port. `upnp.Discovery` sends IPv4 SSDP M-SEARCH queries for MediaServer and ContentDirectory on up to 16 active multicast interface addresses. Replies are bounded to 8 KiB/256 packets per interface and 24 unique descriptions per scan; a 3-second response window and 10-second overall deadline bound the operation. Four description fetches run at once, with 2-second deadlines and the existing 4 MiB XML cap. Concurrent requests share a scan and successful results are cached for 15 seconds.

SSDP LOCATION values must use the existing HTTP(S)/port policy. Description DNS resolves only to the advertising UDP peer; HTTP uses pinned addresses, no proxy and same-host redirects. Loopback/link-local/multicast peers and cross-host descriptions, URLBase and control URLs are rejected. Only devices with ContentDirectory are offered. These are temporary suggestions; no source records, catalogue imports or playback are created by discovery.

The authenticated `/producer/sources/discover` route returns a quiet HTMX fragment for the Add source page. It preserves the form and URL. Selecting a result fills the editable fields; ordinary GET forms provide a no-JavaScript fallback. Saving remains a separate CSRF-protected action. IPv6 SSDP discovery is not implemented; manual description URLs remain available.

Discovery needs local multicast reachability from StationOS itself. Docker bridge networks commonly isolate SSDP from the LAN; an optional Linux host-network override is provided in `compose.discovery.yaml`. It preserves the named data volume. The installation’s Git-ignored `.env` selects the Compose files through `COMPOSE_FILE` and provides `STATIONOS_LISTEN_ADDRESS` (loopback by default) and `STATIONOS_PUBLIC_URL`. The discovery override uses the same listening address for its health check, including when a separate proxy requires a LAN-bound listener. The standard bridge configuration also receives the configured public URL. This keeps site addresses out of tracked configuration and preserves discovery mode through ordinary rebuilds. Host networking is never activated automatically. Manual source URLs work with the default bridge deployment.

### Assistant output-limit recovery

A completed local/native response that explicitly reports output truncation becomes `producer.ErrOutputLimit`, retaining usage counts but exposing no text, items or tool calls for execution. The runner may continue the same saved task with small-step instructions at most twice per run. It checkpoints the existing brief and successful outcomes, never the truncated response, and uses supported recovery generation options through an optional model port. Repeated truncation pauses with the original request intact. HTTP failures, timeouts, malformed/incomplete responses without a confirmed output-limit finish, and live DJ structured responses do not use this recovery path. This is a fresh bounded assistant turn with smaller-step guidance, not a replay of an HTTP request or mutation. Catalogue summaries and exact committed entity references remain protected during budget fitting.

### Assistant action batches

Provider adapters and the runner share a 16-call ceiling. Accepted calls execute sequentially with cancellation checks, intent/outcome audit records and per-call checkpoints; nine to sixteen calls are no longer rejected by a smaller runner-only limit. An oversized completed reply becomes `producer.ErrToolBatchLimit` with its measured usage and no executable calls or saved provider items. The runner requests a smaller reply at most twice per run, retaining the brief and earlier outcomes. The oversized reply is discarded as a whole before any action runs. Repeated oversized replies pause honestly. No model-generated batch is silently truncated or partly omitted.

Assistant show tools publish weekday ranges, quarter-hour minute values and required creation fields in their schemas. Application services accept an end-of-day boundary of 1440 and normalize it to stored midnight 0 without changing the weekday; invalid starts and ambiguous HHMM values remain rejected. Validation retains the `schedule.ErrInvalid` category and supplies field-specific corrections, including missing presenters and playlists belonging to another station. Multi-day creation remains atomic.

Schedule overlap errors identify the attempted slot and the conflicting saved show, including its ID and explicit overnight/week-boundary days. The assistant reads the saved schedule before retrying, reuses existing editions and preserves requested special times. Multi-day conflicts still roll back the entire call; earlier successful tool calls remain committed.

Malformed `create_shows` arguments retain the specific decoder cause (field/type, unknown field or JSON syntax offset) rather than attributing every failure to an array-shaped show. Correction messages distinguish time values from weekday arrays, confirm that the failed call created nothing and preserve earlier committed work. Complete raw arguments are not echoed.

### Measured assistant progress recovery

Resuming a saved multi-step task reads the station configuration before requesting another model response. Station scope comes from its workspace, durable action links and successful recorded outcomes, including records older than the visible message window. Reviews include grouped show editions, unscheduled intervals for all seven days and playlist availability counts. Gaps are observations to compare with requested broadcast hours, not permission to fill unrequested slots. Empty eligibility pools are identified explicitly. Healthy completion reviews omit redundant per-pool details to preserve local-model context space.

After three and eight unchanged rounds, the runner rereads measured configuration and supplies a focused next-action instruction; these bounded recovery attempts do not reset the existing stall limit or replay mutations. Continued failure still pauses safely. SQLite playlist availability aggregates the same genre/album unions, manual inclusions, exclusions and unavailable snapshots as resolution without loading recording metadata. Other repository adapters can use the existing resolution fallback.

Focused recovery narrows the advertised tool definitions to the measured unfinished area (playlist eligibility before schedule gaps). The runner rejects unoffered calls before recording or executing a write. Each read tool has a two-call allowance within recovery, enforced per call even in a batch; a saved configuration write restores the ordinary tool set. For an existing station with actionable work, four rounds of observations cannot keep resetting the progress timer. Checklist rewrites alone never count as configuration progress. These limits select no music, presenter or schedule times automatically: the original authorised brief remains authoritative. Persistent stalls report frequent checks and the most recent action error rather than prescribing another identical Continue cycle.

A Continue following a recorded stall starts with the focused tool set immediately, using fresh measured state, rather than repeating the initial observation allowance. This does not change provider/model settings or retry prior mutations.

Playlist rule tool schemas require an existing playlist ID plus a rule with an explicit `genre`/`album` enum and exact value. Genre rules omit source IDs; album rules require one. Domain validation preserves `playlists.ErrInvalid` while returning the specific field correction. Tool descriptions avoid duplicating the full editorial policy so the stronger schemas still fit the existing local-model context budget.

Producer queue waiting uses the conversation cancellation context rather than a short admission timeout. This prevents a legitimate running local-model request from failing the next producer instruction before generation begins. Live admission deadlines, queue capacity and priority order are unchanged. A queued producer Stop removes its ticket without dispatch; each admitted producer still gets the configured generation deadline. Assistant `TimedText` budgets return zero because queue time is not bounded independently of cancellation. No provider response is replayed and no cloud fallback is introduced.

### Long-session context recovery

Only the latest server-generated schedule readback, progress-recovery instruction and batch-continuation hint of each kind remain in transport context. Arbitrary system instructions, all human requirements and the approved proposal stay protected. If ordinary compaction cannot leave room for a minimal reply, older completed outcomes become a deduplicated action ledger containing exact summaries, URLs, primary identities, deduplicated secondary reference IDs, errors and artwork errors. Camel-case domain IDs and already-compacted identities survive repeated compaction. The full durable transcript and audit records remain unchanged; the ledger explicitly directs the model to reread current configuration before editing. Unanswered exchanges, latest results and the catalogue summary remain protected.

An irreducible context failure is typed and occurs before dispatch. The producer runner may make one fresh attempt with tools restricted to the measured unfinished playlist or schedule area, retaining the saved brief and task. This does not replay a provider response or mutation, change endpoint settings or discard oversized human requirements. Requests whose essential minimum still cannot fit remain safely rejected.

### Binding an unscoped producer work step

Recovery with no usable station/checklist binding offers only `select_work_step` and task progress, rather than reopening the full discovery tool set. The selector resolves the exact existing station name or ID through repositories, supplies measured configuration and restricts tools to one explicit work area. It never changes station configuration. Unknown or ambiguous names fail without creating a station; only the station-creation area may retain a new name for a subsequent explicit create. Selected areas remain active across writes until task progress closes them or the assistant selects another area. Read-only requests can select a review area without gaining write tools.

Observation-only rounds are bounded even in fresh project-wide conversations without a saved checklist. Recovery does not replenish read allowances without a saved configuration write. Successful selections seed an unfinished task if needed, and tool results are delivered before subsequent developer readbacks to preserve provider protocol ordering. Playlist snapshots include pool names, and playlist work can create a requested new pool. The selector reports that release-year metadata is unavailable but explicitly permits musical knowledge plus verified artist/album/track matches. A missing year field alone is not a blocker. Exact album rules or individual track selections can implement era programming; editions and compilation/reissue extras must be assessed, and unavailable music or uncertain release knowledge must be reported honestly.

### Station logo and DJ portrait drafts

Station and DJ forms share a photo editor template and expose generation only when the configured artwork model and
credential are available. Generation uses an injected image port with a bounded
single-request slot and no automatic retries. HTMX replaces only the relevant photo editor;
ordinary forms render the full draft. Generated images are normalized to JPEG and
carried as an unsaved form value and image preview. Save validates the draft again
and persists it through the station or DJ service; generation never writes entity data.
Image data URLs are allowed for these previews; scripts retain their existing CSP.

### Conversational assistant presentation

The web adapter groups consecutive non-user records into one agent turn after
its human request, preserving stored chronology. Live activity, task progress,
actions, recovery controls and readable provider reasoning belong inside that
turn. Event snapshots patch the message region while retaining the composer, focus,
selection and expanded reasoning; they follow the conversation only near the
page bottom. Successful submission returns the canonical conversation URL and
focuses the composer. Ordinary forms remain supported.

Provider adapters expose a bounded display-only Reasoning field from Ollama
thinking, compatible reasoning_content, Anthropic thinking, Gemini thought text
and Responses summary_text. Opaque reasoning items, encrypted content and
signatures are never displayed. No extra reasoning request is made. Readable
reasoning and intermediate commentary are saved as message records; reasoning
is excluded from reconstructed recovery prompts. Existing migrations are unchanged.
Providers return completed responses, so reasoning appears after each model
request completes, rather than token by token. History retains the existing
100-record display window; full human requirements remain independently saved.

## Transmission observations and producer operations

`internal/operations` defines immutable metadata snapshots, incident records and a
bounded asynchronous recorder. Broadcasting emits observations at shared fan-out,
not per HTTP listener and never during read-only dashboard inspection. Music and
speech begin with the first emitted audio bytes; progress checkpoints occur every
240,000 bytes (15 seconds at the fixed 128 kbps output). Final records distinguish
completion, interruption and no-audio failure. They measure server emission, not
confirmed reception or audibility. Speech adapters may report the exact synthesis
text through an optional production context callback; missing text remains unknown.

The recorder serializes writes outside the audio loop. Queue overflow or a failed
write latches a producer-visible incomplete-history warning until restart. Shutdown
stops producers before draining the recorder, allowing five seconds before cancelling
remaining database work. A hard process failure can lose queued observations and
bytes since the most recent persisted checkpoint. Serve startup marks unfinished
transmissions as outcome unknown without inventing an end timestamp. Merely opening
the database for catalogue or integrity commands does not recover records. As with
broadcast ownership, only one serving process may own a database.

Migration 26 creates transmission events and grouped incidents independently of the
rebuildable catalogue. Event sequence breaks equal-timestamp ties. Event snapshots
contain identity and credits, never source URLs, DJ profiles or credentials. Repeated
active incidents share a code per station; acknowledgement is separate from measured
recovery, and recurrence after recovery creates a new incident. Slow listener eviction
is a timeline observation rather than an indefinitely active station incident.

Producer station cards and overview include operational summaries. Timeline and Incidents are dedicated protected station pages. Upcoming selected audio
appears beside current playback in the station Overview with source-proxied album artwork. Timeline browsing uses bounded
keyset pagination, literal search and date/type/outcome/show/DJ filters. Exports are
limited to 1,000 matching records and neutralize spreadsheet formula cells. Timestamps
show station timezone and numeric UTC offset, including repeated autumn hours.
Historical pages remain still and subscribe to fingerprint changes to offer an explicit refresh;
Overview shows only actual runtime selections and the following scheduled programme.
It neither starts production nor requests AI. Selected audio remains revalidatable.

This first release does not add audio recording, exact selection provenance, a durable
future running order, external alerts, aggregate metrics or automatic retention. History
is retained until explicitly managed; include it in database backup/capacity planning.


Producer history renders semantic music/speech outcomes and station activity in a table;
IDs and byte checkpoints remain in storage rather than the visible table or CSV. Event
JSON now carries an optional immutable playlist-name snapshot; old events require no
migration and may show an explicitly labelled current-name fallback. Named show filters
match the saved Show value across per-weekday show IDs. History fingerprints use the
same filtered/keyset-paginated raw records as the rendered page and exclude byte counts
while transmitting. They do not include runtime queues or incident state. Incident pages
fingerprint their own incident records separately. The initial fingerprint comes from
the already-loaded page records, avoiding a second-read race before the first subscription snapshot.


History pages use a counted bounded read in one SQLite transaction. The shared pagination
component preserves history filters and an optional newest-record anchor, alongside
page number and 25/50/100 page size. The anchor bounds the existing (started, sequence)
ordering so newer arrivals do not shift older pages; legacy before cursors remain valid.
Out-of-range pages clamp to the last page. History version checks use the same paginated
records and matching total. Exports clear pagination bounds and retain semantic filters.


## Playlist refresh comparisons and exclusion review

Migration 28 adds `playlist_refresh_items`, an operator comparison baseline without
foreign keys to the disposable catalogue. Existing eligible members are backfilled.
Refresh resolves the saved rule/manual union and compares IDs and source/album pairs
inside one transaction, counts only new available eligible tracks, and replaces the
baseline. Repeating refresh yields zero. Saving selections compares against the previous baseline and refreshes atomically; other membership edits reset the baseline;
source import never invokes refresh. Dynamic rule eligibility is unchanged.

`Playlists.Review` is an editing-only projection containing eligible entries plus saved
exclusion snapshots, stably paginated together. Its eligible count/duration remain separate
from row/exclusion counts. `Resolve`, `Availability` and broadcast pools retain their
eligible-only contract. Re-including a saved unavailable exclusion is allowed while its
availability stays false. Web writes retain authentication, CSRF and restricted return URLs.


## Source genre membership

Migration 29 backfills indexed `catalogue_genres` membership from existing raw catalogue metadata. Commas split genre values; surrounding whitespace and empty parts are removed and duplicate values count once per track. Repeated UPnP genre tags are retained in the source metadata. Catalogue insert/update triggers rebuild membership, and deletion cascades remove it, so successful source refreshes replace genres rather than accumulating local edits. Search filters, playlist eligibility, facets and summary coverage use the same membership. Track counts and duration are never multiplied by genre membership; genre coverage can overlap. Saved playlist rules remain operator choices and are not rewritten.


## Rich dynamic playlist rules

Migration 30 preserves legacy rule IDs/values and expands rule kinds to artist,
composer, year, decade and filter. A filter is a bounded, validated JSON object
of optional genre, artist, composer, album/source and inclusive year bounds. All
conditions in one legacy filter must match. Values within each selected category
are alternatives, and every selected category must match. Years and decades share
a date category. Legacy filters form one additional category. Manual inclusions add
tracks independently; explicit exclusions take precedence. No rules selects no
automatic tracks. Playlist resolution and
Availability share one SQL predicate. Broadcast pools, refresh baselines and the
editing projection reuse resolution, preserving their existing contracts and bounds.

A rebuildable indexed catalogue_credits table splits the existing semicolon-delimited
performer/composer credits, preserves commas in names and is backfilled during
migration. Insert/update triggers replace membership; deletion cascades remove it.
Exact matching never substitutes album artist, conductor or ambiguous creator.
The UPnP adapter preserves dc:date as AudioItem.ReleaseDate and explicitly requests
MinimServer OriginalDate annotations, retained as AudioItem.OriginalDate. AudioItem.Year
prefers a valid original date year, falling back to dc:date (zero means unknown).
Generic UPnP servers retain the standard wildcard filter. These remain source-provided
dates, not independently verified release dates. Missing/invalid dates never match bounded year filters. Existing
catalogues need a source refresh to acquire dates; no migration invents them.

Selections retains atomic checkbox saves across all kinds and preserves unavailable
saved choices. There is no Combined rules selector or creation link. Previously saved filters
remain visible only when present, with edit/remove actions and retained validation
values. Ordinary selection saves preserve these restrictions. Assistant tools expose measured facet values,
credit/year searches and ordinary category selections. They keep source-date and
original-release evidence distinct. No new background subscriptions or audio work are
introduced by rule editing.

### Station music sources

Migration 31 adds station_sources with cascading station/source foreign keys and snapshots every existing station's currently configured sources. New stations default to the sources present at creation; later sources require explicit selection. Station Save replaces a supplied SourceIDs set in the same transaction as settings; nil preserves existing choices, while an explicit empty slice selects none. Unknown source IDs fail atomically. Removing a shared source removes its station memberships.

Playlist resolution, availability, review and broadcast pools apply the owning station's source set before rule/manual eligibility. Saved selections remain unchanged: manually included tracks from unchecked sources are visible as unavailable, and become available again when reselected. Restoring a missing saved track is allowed only while its source remains selected. New individual/album additions reject unchecked sources. Station-scoped catalogue searches and facets constrain picker choices; global source/catalogue administration remains shared. Live broadcasts revalidate their pool at track boundaries, allowing the current track to finish.

UPnP dates are parsed by namespace and meaning, independently of server identity. Prefer `dc:date` over a separate year-only `upnp:date`, retaining unqualified legacy dates and UPnP-only years as fallbacks. Valid `tag.OriginalDate` annotations retain precedence; otherwise accept `upnp:originalDate`. Invalid originals fall back to release dates, and missing dates remain unknown.


## Dated episode planning and editorial sessions

Migration 33 adds optional show musical direction, an explicit varied/artist/album
rotation format, and station/show-owned durable episode JSON. Session identity is
show ID plus UTC occurrence start, independent of schedule revision; stale workers
cannot save after a revision, while producer notes, exclusions, kept recordings and
aired history survive changes to the same dated occurrence. Next week's occurrence
starts a separate session. The adapter checks show ownership and current occurrence
identity in the same transaction as every write and publishes `episodes` only after
commit. Show/station deletion cascades episode state; there is no catalogue FK.

`internal/episodes` owns the provider-independent session, plan validation and a
single cancellable preparation worker. Automatic curation waits for an opened active
episode session, created by presentation preparation or actual playback. It never
prepares future running orders or requires approval. Each job adds one recording behind the ready next track, maintaining at most two
autonomous upcoming choices, retaining theme, prompts, producer constraints, actual aired history and
spoken links. Failed jobs retry after five minutes; abandoned leases expire after
six. Intro/presentation and spoken-link generation share this provider-independent
session; prepared speech remains distinct from actual airtime receipts.

The planner receives up to 30 initial eligible metadata snapshots and can explore
the complete eligible playlist using a bounded JSON search/choose protocol: at most
six turns, 30 metadata results per search, one selected recording and 512 output
tokens per turn. Source transport and artwork never enter model context. Unknown
IDs or durations, exclusions, avoidable performer/album repetition and avoidable
schedule overrun are rejected with feedback. Technical search turns are retained
in storage but not duplicated in ordinary speech/session checkpoints.

Playback receives all currently eligible episode candidates instead of the legacy
ten-item shortlist, follows the first unplayed valid planned recording, and rechecks
eligibility at each boundary. Exact station repeat policy remains authoritative.
Performer/album preferences examine the current recording and four recent recordings
whose metadata is available in the active pool, relaxing oldest first for small pools.
Artist specials allow repeated performers while spacing albums; album specials allow
both. Shared semicolon-separated performers match ignoring case/whitespace; composers
are not performers. Producer exclusions filter local fallback and listener requests as
well as AI choices. Late/failed planning leaves local music selection running.

The dated schedule shows neutral Scheduled, blue Preparing/Updating, green Ready,
amber Delayed (retrying), and grey Cancelled, with text as well as colour. Green requires no human approval. Show clicks
open a dedicated episode detail page in the main HTMX region, including estimated starts and
optional episode direction and a read-only accepted queue. Recurring settings remain a
separate link. Calendar readiness uses SSE; updates preserve scroll, focus and the
separate editing form. Refreshing the running order is explicit so unsaved edits are
not replaced. Optimistic versions reject stale producer submissions.


Migration 34 removes the rotation-format setting. Existing artist/album settings
are translated into DJ music prompt instructions before dropping the column, and
show revisions invalidate affected plans. The planner may explain deliberate
repetition in its episode plan only when requested by show/DJ/producer prompts;
playback honours that editorial decision. Ordinary plans retain variety checks.
The producer form labels musical direction as “DJ music prompt” with explicit
agent-instruction help, rather than presenting a second music-format control.


### Producer assistant editorial and episode tools

The show schema describes `music_direction` as the optional DJ music prompt,
separately from public description and shared DJ personality. `get_show` returns
its full value without the DJ profile; `list_shows` and mandatory grouped schedule
readbacks include a bounded prompt preview and explicit truncation flag. Programme
grouping distinguishes different prompt previews. Partial show updates preserve
omitted prompts, public descriptions and audience settings. The assistant's shipped
instructions call for distinct, catalogue-grounded programme concepts, energy arcs,
discoveries, spacing and deliberate specialist direction, and verify saved prompts
alongside presenter, timing and playlist choices. User-customised shared prompts
remain untouched; capability schemas still document these fields.

When the episode service is available, `list_episodes` reads a bounded page of one
station-local dated week, `get_episode` returns a paginated proposed running order
and recent actual-air receipts, and `save_episode` uses the same optimistic producer
edit service as the UI. Recurring direction uses `save_show`; episode notes, keeps
and exclusions affect only that occurrence. Reads create neither episodes nor AI
work or listeners. Private session transcripts, transport URLs and credentials are
not returned. Mutations report pending automatic replanning, never approval or a
claim of readiness. Episode links target the dedicated HTMX main-content page.

Episode planning in progress is a normal playback state: local music selection
continues without recording a curation failure or resetting planning retry times.
Planning checkpoints tolerate routine speech/history updates, while producer edits
and newly aired music remain protected against stale plan commits. Failed planning
attempts log their underlying cause and report an operational incident, with a
five-minute retry delay and a recovery event after a successful retry. OpenAI
response-body failures identify the endpoint and elapsed time without including
provider payloads, addresses or credentials; timeouts retain their cancellation
cause and follow the existing bounded transport retry policy.

Default episode curation maintains two upcoming choices per live episode, selecting
exactly one additional recording per agent request. Presentation preparation opens the dated session with its DJ and
show prompts; prepared presentation or actual playback enables rolling choices.
No default worker request is made for an unopened or future episode. The rolling
protocol immediately offers up to 30 eligible metadata snapshots, has a six-step
search/validation bound and a 512-token output cap, preserving theme, constraints,
played history and speech while omitting duplicate technical turns. Playback
receipts invalidate consumed choices and notify the single worker to replenish
behind the still-ready next track. Validation includes queued music when checking
duplicates, variety and remaining show time. Mutable template revisions never
change dated episode identity for handover or repeat the introduction.
The process has no full-show planning mode or producer approval gate. Provider options
now reach the OpenAI Responses adapter; no-tool calls omit tool-only controls.
Episode status uses the existing read-only SSE subscription contract independently
of the producer form. Refresh confirms a state read and never triggers generation.

A dated episode's persisted Cancelled flag is a reversible schedule exception,
independent of template revision. The schedule adapter filters those episode keys
before resolving current/next programme, so default music covers skipped dates.
The same cancellation port filters public programmes and audience opportunities;
audience submission rechecks cancellation inside its transaction. Cancellation
requires an unaired future occurrence and optimistic version, increments the
editorial revision to reject in-flight generation, and publishes after commit.
Restore retains the session and constraints. No template or transmission history
is deleted. Producer confirmation uses the shared dialog with a full-page fallback.


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


Between-track speech receives an explicit playout sequence. A handover signs off
the outgoing presenter before the incoming introduction and recording; incoming
track introductions belong to the incoming DJ. This playback contract accompanies
saved custom prompts as well as defaults. Episode checkpoints retain the last six
actually aired speech segments independently of bounded planning turns, so planning
activity cannot erase recent wording/credit context. Prepared speech is distinct
from aired speech; continuity instructions avoid repeating phrases and metadata
credits without inventing new facts. Older checkpoints seed this memory from their
existing aired-speech turns, and new dated episodes start with fresh speech memory.


### Shipped prompt responsibilities

The ten out-of-the-box prompts are UTF-8 text files in
`internal/prompts/defaults/`, embedded by `internal/prompts`. SQL stores only
explicit operator overrides; upgrades do not replace them, and Restore default
removes an override so the current embedded version is used. Human-facing titles
and descriptions live in `prompts.Definitions`; IDs stay stable across wording changes.

Each default describes one task: producer configuration, show opening/closing,
between-track speech, fictional personality, track selection, listener contributions,
single-message screening, multi-message review, logos or portraits. Shared speech
contracts contain metadata-credit rules, recent-airtime continuity and actual
handover order. They accompany both defaults and overrides. Prompt sections are
joined with explicit paragraph boundaries.

The rolling episode planner loads the same shared `selection` prompt as the
legacy selector. Its separate episode protocol describes search/schema fields and
queue invariants; it does not duplicate editorial direction. Production wires the
prompt store into both paths. No migration or bulk rewrite of custom prompts is needed.


## Dated DJ conversations

Every curation request for a dated episode uses one shared conversation key,
independent of template revision. Introduction/sign-off writing, rolling track
selection (including searches and repair turns), and spoken links all continue
that conversation. Requests for the same episode are serialised with cancellable
admission; different episodes remain independent. A new dated episode starts fresh.

Provider continuations send newly aired receipts instead of repeating the whole
aired history; portable replay and recovery supply the complete current checkpoint.
The inference router persists portable user/assistant exchanges and the provider
cursor in SQLite. OpenAI Responses uses stored responses and previous_response_id;
Ollama, compatible, Anthropic and Gemini receive the portable history through their
existing adapters. Changing connection, credentials or model replays portable history
rather than reusing an incompatible cursor. An explicitly expired OpenAI cursor or rejected oversized chain
can be rebuilt once from portable musical memory; uncertain network failures are never automatically replayed.
Task instructions and current DJ/show direction are supplied on every request,
because provider response chaining does not inherit the previous instructions.

Portable history retains every returned choice and spoken link, including chosen
recording credits, while discarding old candidate searches and repeated episode
snapshots. Each current request also supplies the authoritative episode facts:
retained aired receipts (up to 256, including composer), current and queued music,
exclusions, and recent actually aired speech. Prepared output is not proof of airtime.
No provider key, source URL or hidden reasoning is stored in the conversation.
Provider context windows and token charges still apply; oversized history produces
an explicit context failure rather than silently discarding earlier turns. Existing
accepted-next and automatic continuity rules remain authoritative during failures.


### Optional DJ links

Ordinary track transitions request a structured speech field; an empty string is
an explicit music-only decision, not an empty provider response. It makes no TTS
call, creates no aired speech receipt and raises no preparation failure or false
recovery. Current and committed next recordings stay unchanged. The episode
conversation records the decision, and actual aired history lets later links
acknowledge a previously unintroduced recording. Handovers and approved audience
acknowledgements require non-empty speech. Incomplete, malformed and missing
provider responses remain errors. Plain speech from older adapters remains accepted.

Changing music normalisation applies at the next track boundary and invalidates preloaded audio made with the previous setting, without changing the committed recording. With the switch off, the previous fixed-gain playback path is retained.
