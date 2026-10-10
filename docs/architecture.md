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

**DJs** owns reusable fictional presenter profiles (name, model, selected voice, editorial prompt and voice-direction vibe) independently of stations. The domain owns validation and repository ports; no OpenAI calls occur in configuration flows. Persistence stores API keys using AES-256-GCM with fresh nonces and DJ identity as associated data. Metadata reads expose only a saved-key flag; decryption is a separate server-side adapter method. A private 0600 secrets-key sidecar is created beside the database; missing keys with existing encrypted credentials fail startup. Back up the database and sidecar together. Station assignment, script generation and speech production remain separate future boundaries.

**Persistence** uses modernc SQLite, one connection per process, WAL, foreign keys and a five-second busy timeout. This intentionally serialises database work for the initial deployment. Count and search results use one read transaction. Versioned SQL migrations run transactionally, verify applied checksums and reject unsupported newer schemas. Migrations are immutable once released. Frozen versions 1 through 10 fixtures verify upgrades to version 11, preserved identities/metadata, checksum validation and foreign-key integrity. Migration 2 removes source station ownership; migration 3 introduces authoritative station playlists; migration 4 removes saved ordering while preserving playlist membership. Migration 7 adds the selected voice, defaulting existing DJ profiles to Cedar without changing encrypted credentials. Migration 6 adds shared DJ profiles and encrypted credentials. Migration 5 introduces station broadcast assignment and unique editable stream slugs. Its immutable Go backfill runs inside the migration transaction and derives addresses from existing station names. The integrity command checks SQLite storage and foreign keys.

**Web** centres stations: homepage cards lead to per-station Overview/Playlists/Settings. Sources are shared; music browsing opens in playlist context. HTMX replaces the main content inside a stable shell, preserves titles/history, returns HX-Location on writes and falls back to complete ordinary HTML documents. Catalogue filtering/sorting and numbered pagination use ordinary GET URLs; selections use protected forms. Overview and cards poll actual listener/current/next status every three seconds. The player remains outside the main/history swap region. Batch genre/album checkbox selections commit atomically. UI conventions are recorded in user-experience.md. Writes call services and use bounded forms and CSRF tokens. Metadata is HTML-escaped. Authentication is optional on loopback and mandatory on non-loopback startup. The API has one versioned status contract; it does not expose SQL row schemas.

**Operations** use JSON slog messages, health/readiness endpoints, source/station IDs, elapsed import time and explicit import failure logs. Imports are synchronous with two-minute deadlines; there is no background worker, progress persistence or scheduler. Graceful shutdown first cancels station producers and long-lived streams, then drains HTTP requests within five seconds. Source latency is logged per import; fine-grained metrics and persistent source health are planned.

## Authoritative versus derived data

| Data | Ownership | Rebuild behaviour |
| --- | --- | --- |
| stations | Operator configuration | Preserved |
| sources and revisions | Operator configuration | Preserved |
| playlists, playlist_rules, playlist_selections | Operator choices per station | Preserved |
| djs | Shared operator presenter configuration and encrypted credentials | Preserved |
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

This implements playable shared radio with current/next delivery state, but not deterministic programme logs, AI curation, production, crossfades, gapless transitions, recordings, ICY metadata or distributed output. The temporary selector is separate from playlist membership and can be replaced with a curator boundary. Broadcast remains listener-driven rather than running a schedule through zero listeners.

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

The authenticated `/stations/events` endpoint retains one connection per live homepage, bounds concurrency to 64, resets write deadlines for each flushed message, and releases observers on disconnect. Server-rendered station HTML is hashed so unchanged content emits no data event. A quiet 15-second heartbeat also reconciles changes made outside the process and clock-dependent labels. The browser patches only differing nodes while retaining card identity, unchanged artwork and focused controls; navigation closes the connection and history return opens a fresh one. Station workspace polling remains separate.


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
