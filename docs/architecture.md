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

**UPnP adapter** lazily fetches the configured description; supports embedded ContentDirectory services, URLBase and relative control/resource URLs; escapes object IDs; checks SOAP counts; preserves unsupported objects in pagination; parses DIDL into domain items. Every XML response is limited to 4 MiB; calls have ten-second deadlines. Only configured-server DNS-pinned HTTP(S) addresses are reachable, including server-hosted high ports; loopback/link-local and cross-server redirects are rejected. OpenStream freshly resolves audio and opens it through the pinned transport. Its supported demuxer is explicit; manifests and cross-server targets are rejected. Conversion remains in the broadcast adapter.

**Catalogue** is a shared local derived index separate from sources and accessible to every station. Sync asks adapters for a complete catalogue enumeration root when available. MinimServer uses its verified unfiltered `0$items` view rather than recursively expanding virtual facet menus; generic UPnP sources retain bounded root/container traversal. Sync traverses the chosen root/container pages, handles cycles/repeated objects, deduplicates identities, bounds time/pages/containers/items and total metadata bytes (64 MiB per import by default) and verifies per-container UpdateID/count consistency. It normalises identity and validates content before writing. A complete snapshot is atomically replaced only if the source revision is still current. A failed fetch or database transaction preserves old items. Successful replacement removes source orphans. Station/source records are never part of clear/rebuild operations. Snapshot stability across different containers and incremental change tracking remain future work.

**Playlists** owns station-specific identity, description, dynamic genre/album rules, individual inclusions/exclusions. Rules resolve as a union against the shared catalogue; exclusions win and overlaps are deduplicated. Album identity is currently source plus exact album title, so same-title editions in a source are grouped. Genre identity is the exact imported value. New rule matches join automatically on the next catalogue import. Manual selections save content snapshots without foreign keys to disposable catalogue rows, so missing items remain visibly unavailable. A playlist is an unordered eligibility pool for human/AI curation. Sorting by title/ID supplies stable browsing pages and implies no broadcast priority. Resolution and mutations are transactional and bounded (100 rules, 10,000 entries). Playlist definition is separate from a future timed programme log or playback engine.

**DJs** owns reusable fictional presenter profiles (name, selected voice, editorial prompt and voice-direction vibe) independently of stations. Credential management belongs to shared AI settings in the producer domain. Profile metadata reads derive only a shared-key-present flag; DJ saves cannot set or clear credentials. The encrypted shared key uses AES-256-GCM with independent associated data and the database's existing 0600 `.secrets-key` sidecar. Back up the database and sidecar together. Missing sidecars with encrypted credentials fail startup.

**Persistence** uses modernc SQLite, one connection per process, WAL, foreign keys and a five-second busy timeout. This intentionally serialises database work for the initial deployment. Count and search results use one read transaction. Versioned SQL migrations run transactionally, verify applied checksums and reject unsupported newer schemas. Migrations are immutable once released. Frozen versions 1 through 10 fixtures verify upgrades to version 11, preserved identities/metadata, checksum validation and foreign-key integrity. Migration 2 removes source station ownership; migration 3 introduces authoritative station playlists; migration 4 removes saved ordering while preserving playlist membership. Migration 7 adds the selected voice, defaulting existing DJ profiles to Cedar without changing encrypted credentials. Migration 6 adds shared DJ profiles and encrypted credentials. Migration 5 introduces station broadcast assignment and unique editable stream slugs. Its immutable Go backfill runs inside the migration transaction and derives addresses from existing station names. The integrity command checks SQLite storage and foreign keys.

**Web** centres stations: homepage cards lead to per-station Overview/Playlists/Settings. Sources are shared; music browsing opens in playlist context. HTMX replaces the main content inside a stable shell, preserves titles/history, returns HX-Location on writes and falls back to complete ordinary HTML documents. Catalogue filtering/sorting and numbered pagination use ordinary GET URLs; selections use protected forms. Overview and cards poll actual listener/current/next status every three seconds. The player remains outside the main/history swap region. Batch genre/album checkbox selections commit atomically. UI conventions are recorded in user-experience.md. Writes call services and use bounded forms and CSRF tokens. Metadata is HTML-escaped. Authentication is enabled only when `STATIONOS_ADMIN_PASSWORD` is non-empty, regardless of the listen address. An unset or empty password disables login for management routes; CSRF tokens and origin checks still protect writes. The API has one versioned status contract; it does not expose SQL row schemas.

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

The optional source `ArtworkReader` retrieves image URLs from known current/next metadata using the adapter’s checked transport. The web edge exposes authenticated station/item artwork URLs and never accepts an arbitrary URL from a request. Raster format, size, timeout and concurrency bounds apply; private caching avoids repeated transfers during card polls. Card presentation combines configured default assignments with immutable runtime programme/current/next snapshots and does not infer on-air state from a schedule clock.

## Prepared audio transitions

Per-track DJ links are generated while the current track plays. Ready speech is emitted through the same paced fan-out at the music boundary; cancelled speech workers and source/process cleanup are joined only when the station stops, rather than stalling the next segment. Each station admits one speech-generation worker, including cancellation cleanup, so slow cancellation cannot accumulate AI requests.

The optional `broadcast.PreloadingEncoder` primes the queued song in parallel with current playback. FFmpeg retains one 4 KiB output chunk and relies on bounded pipe backpressure for further decoding. One current and one queued encoder are shared by every listener, rather than multiplying work per connection. The queued item and show are revalidated before use; changed or unready preparations are cancelled. Last-listener cancellation releases both encoders and joins their workers. Source failures and late AI still use the existing music/retry path.

Presenter normalization removes only leading/trailing near-silence, preserving pauses within speech. The presenter cache key versions this audio contract, so old padded introductions/outros are regenerated. MP3 format, bitrate, sample rate, fan-out pacing and listener connections remain consistent across music and chatter. Decoder/process errors are reported during asynchronous cleanup without inserting a process-wait delay into the stream.

## Recording credit roles

UPnP artist/author entries are multi-valued, role-bearing credits. Preserve track performers in `AudioItem.Artist`, with separate Composer, AlbumArtist, Conductor and ambiguous Creator fields in catalogue JSON. Composer-role entries never overwrite performers, and an ambiguous creator is not relabelled as a performing artist. Multiple credited performers/composers are deduplicated without dropping their names. No schema migration is needed: source refresh rebuilds derived metadata and playlist resolution uses the current catalogue entries.

Both DJ curation candidates and current/next chatter inputs use explicit Performers and Composer labels, alongside album-level and conductor credits. Instructions distinguish who performed a recording from who wrote the work, allow optional composer commentary and prohibit invented missing credits. Presenter cache keys version the credit contract so cached speech cannot retain the previous attribution.


## Event-driven station overview

Broadcast runtime observers use bounded, coalesced notifications for displayed state/listener changes; ordinary audio frames do not notify when state is unchanged. Successful management writes announce committed configuration changes. Observers are independent of audio subscriptions and cannot start a station or change listener counts.

The authenticated `/producer/stations/events` endpoint retains one connection per live homepage, bounds concurrency to 64, resets write deadlines for each flushed message, and releases observers on disconnect. Server-rendered station HTML is hashed so unchanged content emits no data event. A quiet 15-second heartbeat also reconciles changes made outside the process and clock-dependent labels. The browser patches only differing nodes while retaining card identity, unchanged artwork and focused controls; navigation closes the connection and history return opens a fresh one. Station workspace polling remains separate.


### Persistent player metadata

The shell player shows the actual current and next titles and performing artists, with DJ on air during speech. A station-specific authenticated event connection updates only changed metadata and remains active across HTMX navigation. It does not start audio or count as a listener; switching stations replaces the connection and Stop closes it. Empty runtime states show Nothing playing and Not queued. The layout wraps for smaller screens.


## Bounded streaming reserve

The serve command configures a five-second `StreamBuffer`. A delivery clock permits a bounded startup/catch-up burst and then real-time 16,000-byte/s MP3 output. Supply stalls no longer reset the clock after one second: short deficits are repaid, while recovery after a prolonged outage is capped at the configured lead. The station keeps a byte-bounded recent window (up to 80,000 bytes, at most 64 chunks). Listener admission snapshots that window into one initial queue entry under the same lock as subsequent fan-out, avoiding a hole or duplicated audio at the join boundary. The same audio feeds all clients; no listener starts a separate producer.

Each FFmpeg stream uses a cancellable 32-chunk read-ahead channel, normalising pipe reads into 4 KiB output blocks and preserving the final partial block/errors. A single in-flight read plus the channel holds at most about 8.5 seconds of encoded bytes per reader; the current and preloaded next item remain bounded separately. Backpressure bounds further source consumption. MP3 format and bitrate stay unchanged; no fake keepalive bytes are inserted into audio. Starved supply reads over one second and slow-listener eviction are logged. Physical radio and deployment-network acceptance remains necessary; outages beyond the reserve cannot be guaranteed seamless.

Schedule resolution and remaining-time planning use the audio timeline at track boundaries, accounting for bytes sent ahead of wall-clock playback. A startup burst cannot add an extra outgoing track just because its delivery ends before its audible show boundary.


## Station mixer

`internal/mixer` owns separate station music and DJ amplitude percentages (0–400%, default 100%). Migration 10 adds authoritative station mixer configuration with deletion cascading; an absent row retains unity gain. Both levels save atomically, independently of station details and playlist assignment. Mixer controls commit through protected JSON autosave requests without navigation or form swaps; the browser serializes writes and retains the latest pending levels, with inline retry on failure.

Music gain and peak protection run in the source FFmpeg encoder. The broadcast manager reads levels at track boundaries and rejects preloaded audio prepared with an older music level. Changes do not interrupt the current song or reconnect listeners. Prepared speech is gently compressed and levelled to -14 LUFS/-2 dBTP before caching (audio contract version 4), following Retro Radio's `agentSpeechFilter`. DJ gain is applied to cached speech locally at airtime, after levelling and before MP3 encoding, without changing shared assets or making new AI calls. Clamping protects amplified peaks without automatic loudness processing that would reverse the adjustment. StationOS retains sequential music/speech segments and its existing bounded fan-out.

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

The producer audience queue explains that review runs during spoken-link preparation. Its results refresh quietly every five seconds, preserving filters, pagination, scroll and focused actions. Tick/X icon actions retain accessible names and tooltips. No-listener and missing-shared-key notices reflect measured runtime state. AI review failures are logged without listener message text; failed reviews remain pending.

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
100 messages, with older conversations retained at their URLs. Polling updates only
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
while preparation runs. Pending or successful plans are not requested again.
Success clears the failure state, with the ready plan used at a subsequent music
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
Local curation can produce an eligible validated plan without speech credentials;
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
connection response deadline for an admitted request to finish, while producer
work has a 30-second admission limit. Each admitted request then has
a configurable 15–600 second response deadline (180 seconds for private/native
connections; existing OpenAI requests retain their 60-second limit), cancellation, a 1 MiB response bound, and configured context/output
budgets (default 16384/2048, context range 2048–32768). Input token counts are
estimated at three UTF-8 bytes per token, including tools; essential requirements
are never trimmed to fit. Overflow pauses explicitly with original requests saved.
Ollama responses at the reserved input boundary are rejected before tool execution.
Exact tokenizer-independent preflight counts are not available for every provider.
Local curation offers at most 64 compact candidates per preparation, identified by short request-local references. The response schema enumerates exactly these references; validation rejects unavailable or repeated choices before mapping them back to catalogue IDs. A completed local plan with invalid choices receives at most one corrective text request within the existing preparation deadline. Transport failures, incomplete responses and invalid JSON are not replayed, and speech starts only after validation. Music reads
never acquire the inference gate, and existing pending/success deduplication and
song-boundary preparation retry remain authoritative.

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
connection limit. Producer assistant thinking remains at the selected model's
default. Frozen version 23 and 24 fixtures verify both historical upgrades.


Live inference admission is sized against the same bounded connection deadline
as the active request, rather than expiring every queued link after 30 seconds.
The production budget includes this wait, a full generation deadline and speech.
Song-boundary cancellation removes stale queued links without dispatching them;
last-listener cancellation releases all outstanding work. Within the eight-ticket
queue, short spoken links and audience review are admitted before show preparation,
then producer conversations. Running calls are never preempted or automatically
replayed, and music never waits for the inference slot. Queue expiration logs include
the actual wait limit; a shorter parent deadline can still end an admission early.

Speech readiness and curation readiness are separate broadcast state fields. A ready spoken link remains playable when show curation fails; both statuses and AI notices participate in live update comparison.

The Assistant navigation separates endpoint management at `/producer/assistant/endpoints`, feature routing at `/producer/assistant/settings`, and chats at `/producer/assistant`. Historical settings connection URLs continue to render endpoint forms. Endpoint credential changes preserve all role routes and model IDs, using the existing encrypted configuration store and CSRF/origin protections.

Model discovery uses read-only provider listings with a ten-second deadline. The CSRF-protected endpoint save validates its configuration and model-list response before committing. Failed discovery cannot modify the saved endpoint or credential. OpenAI credential replacement also validates discovery before saving; explicit credential removal remains available. Saved endpoint pages and feature model fields fetch lists automatically. Feature model fields fetch suggestions from saved endpoints and keep manually entered selections. Discovery never invokes capability tests, inference, downloads, or model substitution.
