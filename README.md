# StationOS

StationOS is an independent, modular radio station operating system built with Go, SQLite, server-rendered HTML and HTMX. It manages stations and their playlists against one shared content catalogue. Retro Radio remains a separate receiver/home-audio product.

## Current status

StationOS now provides **listener-driven station streams**. Create and edit stations, configure shared UPnP/MinimServer sources, import metadata, browse the full library and build station-owned playlists from whole genres, albums and individual tracks. Genre and album selections automatically include new matching tracks after a source import. Imports preserve previous data on failure; successful rebuilds retain station, source and playlist configuration. Assign a station playlist, connect to its stream URL or choose Listen in the app. Every listener shares one programme. Weekly shows select their DJ and playlist in station-local time. DJs curate eligible music and prepare spoken links off the playback path; a music fallback keeps broadcasts running when preparation is unavailable.

## Build and run

Requires Go 1.27.2 or later and FFmpeg on PATH (`brew install ffmpeg` on macOS; included in Docker). The project, CI and Docker build use Go 1.27.2. With automatic toolchain selection enabled, an older Go installation downloads the required version when you run project commands. SQLite needs no native library or CGO. Initial dependency download requires network access; the normal tests need only local resources and loopback HTTP listeners.

```sh
make build
./bin/stationos
```

Open http://127.0.0.1:8080. The default database is `data/stationos.db`; it is created with explicit embedded migrations. Timezone data, templates, CSS and HTMX are embedded in the executable.

1. Create your stations from the homepage. Each station has its own card and workspace: **Overview**, **Playlists** and **Settings**.
2. Add music servers under **Sources**, using each server's actual UPnP device-description URL. Sources are shared across all stations. The UI example is illustrative; obtain the description URL from the server's UPnP advertisement/configuration.
3. Open the source and choose **Refresh library**.
4. Browse **Add tracks** within a playlist with search, genre/album filters, sorting and 25/50/100 items per page. Details include artist, album, genre, duration, format and source. Numbered pages, first/last links and page jumps preserve filters.
5. Open a station and choose **Playlists**. Create a playlist, tick any combination of genres and albums in searchable lists, then **Save selections**. Use catalogue checkboxes to add individual tracks. Each station can own multiple playlists.

6. In station **Overview**, select its **Default playlist** and save. Copy the stream URL for devices, or choose **Listen**. The player remains during HTMX navigation and browser Back/Forward; Pause and Stop release the listener connection.

Playlist selections combine automatically across categories. **Pop + 1980s gives 1980s pop.** Multiple values within a category are alternatives: Pop + Rock and 1980s + 1990s gives pop or rock from either decade. Years and decades share one date category. Empty categories add no restriction; no selections means no automatically included tracks. Select exact genres, albums, performing artists, composers, source-reported years and decades using the searchable checkbox lists. Newly imported matches join automatically. Individually added tracks remain additions, and exclusions take precedence.

There is no separate combined-rule selector. Previously saved advanced restrictions remain visible only on playlists that have them, can be edited or removed, and also restrict the selected categories; any one saved advanced restriction must match. Saving ordinary selections preserves them.

Dates use the year of the UPnP `dc:date` value, which may be a reissue date rather than the original recording/release. Missing or invalid dates remain unknown and never match a year condition. Refresh each source after upgrading to import dates. MinimServer must expose the relevant tagged metadata; StationOS does not infer dates. Artist and composer rules match individual role-aware credits exactly; commas inside names are preserved. Album-artist and ambiguous creator credits are not treated as performing artists.

Removing a track creates an explicit exclusion, including from all dynamic rules; adding it again removes that exclusion. Playlists are unordered pools of eligible music. The DJ curator chooses the broadcast sequence; alphabetical title sorting is only for browsing. Rules, manual choices and exclusions survive catalogue rebuilds. Missing manually selected items retain their saved metadata and show as unavailable. Albums are identified by source and album title; identically titled editions within one source currently share a selection. Genres match the exact imported value (for example, `Jazz` and `jazz piano` are separate choices).

**Upgrading an existing installation:** stop the old process, then run `make run` (or rebuild and start the executable). Startup applies migrations through version 9 automatically, preserving station/source IDs and catalogue data while making sources shared. Back up the database first; migration 1 is unchanged. Do not run the old executable against an upgraded database.

Imports copy metadata and resource addresses only; audio stays on the UPnP server. MinimServer imports use its flat all-items view, avoiding repeated virtual artist/genre/date menus.

Sources must resolve to a trusted server address. Loopback, multicast, link-local and unspecified addresses are rejected. DNS is pinned during each import, resources and redirects remain on that server, and proxy environment variables are ignored. Rebuild to pick up address changes. Playback freshly resolves each source object through the same checked transport. Only checked audio bytes reach FFmpeg; no music files are copied into the database.

The UI uses HTMX for navigation and writes within a stable application shell, with real URLs, back/forward history, save feedback, loading/error states and ordinary-form fallback. Station overview subscribes to measured runtime changes through server-sent events. Homepage cards and the persistent player use live events and update only changed content; editing forms remain stable. The player shows current and next titles and artists as you navigate.

## Test and develop

```sh
make check     # tests, vet, formatting check
make race
make integrity
make sync SOURCE=<source-id>
make clear-catalogue
```

`clear-catalogue` deletes derived metadata only. To use a different database or clear one source:

```sh
./bin/stationos -db /path/to/stationos.db -task integrity
./bin/stationos -task clear-catalogue -source <source-id>
./bin/stationos -task sync -source <source-id>
```

Imports are full source reconciliations, not incremental synchronisation. Catalogue and playlist pages offer 25, 50 or 100 items. A playlist supports up to 100 selection rules and 10,000 resolved entries; these are explicit foundation limits. SQLite's case folding is ASCII; broader locale-aware search is planned. Back up the database with SQLite's online backup tooling or stop the process before copying its database and WAL files.

## Docker

```sh
# Optional: enable login with username admin.
export STATIONOS_ADMIN_PASSWORD='choose-a-strong-password'
docker compose up --build -d
```

Open http://127.0.0.1:8080. When `STATIONOS_ADMIN_PASSWORD` is set to a non-empty value, sign in as `admin` with that password. If the variable is unset or empty, authentication is disabled and the management UI and API are accessible without login. Compose publishes to host loopback and retains `/data` in a named volume. The runtime runs as a non-root user. Container health checks use readiness. Configured UPnP sources use outbound HTTP. Automatic server discovery additionally needs local SSDP multicast reachability; see the discovery deployment notes below. Docker Desktop/container networking must be able to reach that server.

The password is optional for local execution too, including when binding beyond loopback:

```sh
STATIONOS_ADMIN_PASSWORD='choose-a-strong-password' ./bin/stationos -listen 0.0.0.0:8080
```

The initial management UI is intended for a trusted host/network. Basic authentication over plain HTTP is suitable only for local/private access; use an SSH tunnel for remote administration. Forwarded headers are deliberately not trusted. For an HTTPS reverse proxy, set `-public-url` to the exact external origin (for example, `https://radio.example.com`). This origin is also allowed for management writes when the proxy connects over HTTP; forwarded headers cannot add trusted origins. Set `STATIONOS_ADMIN_PASSWORD` to enable authentication when needed; omitting it gives anyone who can reach the app access to management. Form tokens and cross-origin write checks remain enabled in both modes. Multi-user roles and a hardened public internet deployment remain future work. Health endpoints reveal only availability and are unauthenticated. Writes require a form token and reject cross-origin requests. Secrets are supplied by environment, never source control.

## Documentation

- [User experience standards](docs/user-experience.md)
- [Architecture](docs/architecture.md)
- [Retro Radio inspection and boundary](docs/retro-radio-boundary.md)
- [Development status and verification](docs/development-status.md)
- [HTTP API](docs/api.md)
- [Architecture decisions](docs/decisions/)

## Licence and provenance

GPL-3.0-only; see `LICENSE`. The scoped UPnP transport was selectively copied from Retro Radio with its existing GPL notice, without a dependency on that repository. The vendored HTMX distribution retains its Zero-Clause BSD licence in `internal/web/static/HTMX-LICENSE`. Other source was newly written against StationOS-owned contracts. See `NOTICE.md` for provenance.

Playlist editing uses separate Selections, Tracks and Details tabs. Stations and Sources are the main navigation; track browsing opens from a playlist. Add playlist and Add source open dedicated creation forms. Pagination uses one row above the tracks.

Sources have Overview and Settings tabs with imported library counts and a contextual refresh action. Playlist Details and station Settings include deletion with styled confirmation dialogs. Deleting a station atomically removes its playlists; shared sources and catalogue items remain. Unsaved-change prompts use the same dialog style.

## Station broadcasting

Each station has a unique editable stream address, generated from its name (e.g. `/streams/reni-radio`). Renaming preserves the address; explicitly changing it requires listeners to update their presets. Existing stations receive name-based addresses in migration 5. Duplicate generated addresses receive an identity suffix; duplicate custom addresses are rejected.

The first GET listener starts one producer and one encoder for that station. Later listeners join its live point rather than starting their own tracks. The last disconnection cancels fetching and encoding. HEAD only advertises the representation. The stream is continuous 128 kbps MP3, 44.1 kHz stereo with no seeking or Content-Length. All audio files currently pass through a single station encoder for a consistent output format. There may be short gaps between tracks; gapless transitions and crossfades are future work. Current/next metadata describes server delivery; devices may buffer ahead of audible playback. Counts represent active stream connections, including app playback.

Audio producers, fan-out and HTTP handling use Go goroutines; FFmpeg does codec work in a bounded separate process with one encoder thread. One immutable chunk is shared among listeners, with 32 bounded queue slots per listener (at most about eight seconds), rolling write deadlines and slow-listener eviction. Default limits are 1,024 listeners per station and up to eight active stations, limited by CPU count; configure `-max-listeners` and `-max-broadcasts` for the deployment. These are admission limits, not measured hardware capacity guarantees. At 128 kbps, 1,000 listeners need approximately 128 Mbps of payload bandwidth plus overhead.

For devices on your LAN, bind to the LAN interface and set an advertised origin if needed:

```sh
STATIONOS_ADMIN_PASSWORD='choose-a-strong-password' ./bin/stationos -listen 0.0.0.0:8080 -public-url http://your-server:8080
```

Management remains password protected when configured; GET/HEAD `/streams/<address>` is public for external-player compatibility. The default listener and Compose host publishing remain loopback. `-public-url` sets displayed stream URLs and the explicitly trusted public origin for management writes; it does not change routing, TLS or network exposure. Browsers need JavaScript for the persistent player; ordinary management forms retain their fallback. The app does not start playback automatically. Changing the assigned playlist or deleting it disconnects the current broadcast; reconnect to start the updated pool. Membership changes and newly imported genre/album matches take effect at the next track boundary.


## DJs

Choose **DJs → Create DJ** to save a fictional name, voice, prompt and voice-direction vibe. All DJs use the curation/writing provider and model selected centrally in **Assistant settings**; DJ forms have no model overrides. **Use example vibe** inserts sample voice directions. Speech independently uses the model selected in Assistant settings (default `gpt-4o-mini-tts`) with each DJ's voice, vibe and conversation speed. The speed slider ranges from 0.25× to 4.00× and defaults to 1.00×. Saving profiles makes no inference requests; listening starts background preparation through the selected providers. Upgrading removes old DJ model overrides while preserving global model choices, profiles, artwork and schedules.

The shared OpenAI key is configured once under **Assistant → Manage endpoints → OpenAI**. It is encrypted and never displayed: a blank edit keeps it; enter another to replace it or select **Remove saved key**. DJ profile edits cannot change credentials. Keep the database and its `.secrets-key` file together when backing up/restoring. The sidecar is generated automatically with owner-only permissions on server startup and must remain private. Encryption does not protect against access to both files or the running server. Startup refuses to create a replacement sidecar if encrypted keys already exist. Profiles are preserved when rebuilding the catalogue.

## Weekly schedule and DJ curation

In a station, choose **Schedule → Add show**. Select a weekday, start/end times in 15-minute steps, a shared DJ, and one of that station’s playlists. Shows repeat weekly in the station’s timezone; an end time at or before the start finishes the following day. Overlapping shows are rejected, including Sunday-to-Monday overlaps. Use the scrollable weekly calendar or **Show list** to edit a slot. A DJ or playlist used by a show must be replaced in that show before it can be deleted.

Three editable starter DJs are added once: **Alex Bright** (upbeat Pop, Coral voice), **Miles Mercer** (relaxed, informed Jazz, Onyx voice), and **Clara Vale** (warm, thoughtful Classical, Sage voice). Each has a curation/chatter prompt and voice-direction vibe. Add the shared key in Assistant settings to enable AI curation and speech for every DJ. Upgrades preserve existing profiles; restarting preserves edits and deletions of the defaults.

The first listener activates the current show. Music continues from the station’s default playlist during gaps; assign a playable default to keep these gaps covered. The current track finishes before handover, and the same open MP3 connection carries the outgoing presenter, next show and music for every listener. A show that ended while a long track played is skipped. Schedule edits take effect at track boundaries. Stopping the last listener cancels production and fetching.

The DJ uses its editorial prompt and musical knowledge to curate a bounded set of up to 64 eligible tracks from up to 256 sampled candidates, with title, artist, album, genre and duration. These choices are a transient show plan, separate from unordered playlist membership. Near the end, local duration planning uses known lengths and short combinations to reduce overrun; unknown lengths and decoder delays prevent an exact timing guarantee. Ready spoken audio is included in the remaining-time budget. Between tracks, the DJ receives the current song, queued next song and handover target; source URLs and API keys are not included in that editorial input.

Responses and speech are prepared in the background, bounded by time/response-size limits. Saved plans and normalized intro/sign-off audio are cached per occurrence/profile and handover, with at most 64 cached records (each audio segment at most 1 MiB). Per-track links are prepared while music plays and are used only if ready and still relevant. Missing keys, failed requests or late assets produce music-only fallback; the API cannot hold up a timing decision. Speech and music share 128 kbps, 44.1 kHz stereo MP3 framing. Source-opening latency and MP3 encoder padding can still cause brief audible gaps; crossfades and sample-perfect gapless mixing are not implemented.

Clock-injected tests verify finishing tracks, sharing handovers without reconnecting, weekly/default resolution, overlap handling and daylight-saving boundaries. Repeated autumn boundaries use their first occurrence, so adjacent shows share a boundary; missing spring times advance to the next valid minute and collapsed shows are omitted. API tests use fake responses and do not spend API credit. A durable execution audit and full deterministic programme log remain future work.

## Producer station cards

The homepage now shows each station’s active programme, DJ, local show times and playlist; stations without a running show show the configured default playlist. Current and next tracks include artist, album, duration and artwork where available. Cards also show measured listener count, DJ preparation/failure or missing-key state, finish-track/handover status and the following show. Dormant stations remain clearly empty. DJ and playlist names link to their settings; Listen uses the persistent player.

Cards receive quiet event updates without stealing keyboard focus, and card navigation opens the full station workspace. Artwork is served through StationOS from the source’s checked transport, with an eight-second timeout, a 4 MiB raster-image limit, four concurrent requests and private browser caching. Missing artwork uses a music placeholder. No database migration is required for this UI change.


## Stream buffering and hardware radios

The server defaults to `-stream-buffer 5s` (configurable from `0s` to `5s`). The first listener receives a bounded startup burst, followed by 128 kbps delivery. Later listeners receive approximately the most recent five seconds from the same programme, then join ongoing delivery atomically. Short supply stalls retain their catch-up budget so the device can rebuild its reserve. Device buffering determines audible latency; current/next metadata reflects server delivery.

FFmpeg output is read ahead through a bounded eight-second encoded buffer for the current and queued tracks. This absorbs source/encoder jitter without fetching or storing whole songs. Recent audio is shared per station; slow listeners remain bounded and cannot block others. Extended source outages or a persistently slow network can still exhaust buffers. Logs report `Audio supply stalled` and `Slow stream listener disconnected` for diagnosis.

For Nginx Proxy Manager, leave response caching disabled for the station proxy. StationOS sends `X-Accel-Buffering: no` on streams and live events. If custom proxy settings override this, pass through `/streams/`, `/radio-events`, `/radio/...` and `/producer/stations/...` event responses without buffering or compression. To isolate device/network/proxy faults, compare the same station's HTTPS stream with its direct LAN HTTP stream. The management password is not needed for either stream URL.


### Public radio and producer access

The public homepage is `/`; management is `/producer`. Individual listener pages use `/radio/<stream-address>`, and the existing MP3 feeds stay at `/streams/<stream-address>`. Older management bookmarks redirect to their new producer addresses. Use `STATIONOS_ADMIN_PASSWORD` on an internet-facing deployment and keep `-public-url` set to the public HTTPS origin; leaving the password empty retains the optional unauthenticated management behaviour.

Station Settings → Public visibility controls publication: Listed appears in the directory, Unlisted works through direct page/stream links, and Private is available only through producer preview. Existing and new stations default to Unlisted, so an upgrade does not automatically list stations or break existing public stream URLs. Changing a station name preserves its addresses; changing Stream address deliberately changes both page and feed. Public pages include live player metadata, weekly programmes and a copyable internet-radio URL. Opening a page never starts production.

### Producer assistant

Open **Assistant** inside `/producer`, or a station's **Assistant** tab. In
**Assistant settings**, choose a Responses-compatible model and supply the
shared OpenAI API key. It serves every AI service and is encrypted with the
database's existing `.secrets-key` sidecar. Choose independent models for producer
chat, DJ curation/writing, speech and audience review. The DJ model applies to
every presenter and is managed only here.

The assistant can explore imported music and manage sources, stations, playlists,
DJs, weekly shows, default playlists, mixer levels and audience decisions on the
current producer's behalf. It uses the same optional login and protected form
writes as the producer app. Changes appear as saved action records with links;
conversations survive navigation and restart. Long requests continue automatically in batches and checkpoint their brief and
remaining work. Completed actions stay saved if a later response fails. Use
**Continue saved request** to resume an interrupted request without repeating its
requirements; **Stop assistant** cancels an active run. New DJs use the shared key.

No external MCP endpoint is enabled. Producer capabilities and their schemas are
shared independently of the UI and model adapter, ready for a future MCP adapter.

The producer assistant automatically creates station logos and fictional DJ portraits
for new entities. Its artwork model is configured in Assistant settings and uses the
same shared OpenAI key. Each asset uses one square image at low generation quality;
image generation is separately billable. Existing artwork stays in place unless a
replacement is requested. A failed image does not undo station/DJ creation, and
missing artwork can be retried on the existing entity.


## Local and privately hosted AI

In **Producer → Assistant → Manage endpoints**, add a named endpoint, then select its models under **Models for features**. Choose
native Ollama, OpenAI-compatible chat completions, native Anthropic Messages or
native Gemini generateContent. Existing OpenAI settings survive upgrades. Other
services work through their compatible protocol; a new wire protocol requires an
adapter. StationOS executes its own validated tools, not a provider-hosted agent's
remote actions.

For Ollama use its server origin, such as `http://your-mac:11434`, without `/api`.
For a compatible server use its API base, commonly `http://your-server:port/v1`.
Anthropic's public API base is `https://api.anthropic.com/v1`; Gemini's is
`https://generativelanguage.googleapis.com/v1beta`. Credentials are optional for
private services and are stored encrypted once per connection. OpenAI credentials
are never reused for another provider. Back up the database and `.secrets-key`
together, including when using private-provider credentials only.

Choose the provider and exact model independently for producer, DJ and audience
text; keep OpenAI selected for speech and artwork. Enter `gemma4:12b` manually
only if that exact model is installed on your Ollama server. Discovery confirms
the provider’s available model IDs. StationOS never downloads
models or silently substitutes global model choices. All DJs use the global
curation/writing provider and model; historical profile overrides are removed
on upgrade without changing the selected global model.

The initial context/output budgets are 16384/2048, with one concurrent inference
request and a bounded queue. These are conservative application bounds, not a
promise that a model fits 18 GB of memory. Reduce the context budget if memory
pressure occurs; a large producer request may then pause rather than discard its
requirements. Prompt token counts are estimated before sending and provider
limits/capabilities vary. Live DJ work takes priority over queued producer work;
slow or unreachable inference leaves music running with existing boundary retries.

Each AI feature uses only its selected endpoint and model. Provider failures never switch to another endpoint or cloud service. Historical fallback configuration is ignored.

The address must be reachable **from the Debian Docker container**. Its `localhost`
is not your Mac. Use a stable private LAN/VPN address or hostname, keep Ollama
behind your private network, and allow access only from the StationOS host. Do not
publish Ollama through the public station proxy. Configure Ollama's listening
address through its supported setup for your installation; StationOS does not
change network listeners, firewalls, model installations or running services.

For guided Mac/network setup or deployment, proceed one step at a time and verify
each result before continuing. Connection checks run in StationOS's execution
environment, so they are the final evidence of container connectivity.


Private inference connections have a configurable **Response deadline** (15–600
seconds; default 180). The timer starts after admission to the single inference
slot; producer queue waiting is separately bounded at 30 seconds. Live DJ work may
wait up to the saved response deadline; stale links are cancelled at song boundaries.
Queued short links and audience review take priority over show planning. Music continues during
slow inference, and failed preparation remains eligible for the next song-boundary
retry. Logs distinguish queue expiry from a response timeout. A timeout never
silently triggers cloud fallback or replays an ambiguous request.

For Ollama DJ text and local audience review, StationOS reads the model's thinking
metadata and requests `think:false` only when that value is advertised. This avoids
unnecessary reasoning before a short spoken link; producer assistant requests
retain the model default. Spoken links and audience reviews use at most 512 output
tokens, bounded further by the saved connection limit. These controls improve
resource bounds but do not guarantee that a model finishes before a song ends.

DJ status reports speech and music curation separately. “Speech ready” can appear alongside “Curation unavailable · music continues”; prepared chatter still plays at the track boundary. Local curation uses short track references and makes one bounded correction attempt when a completed plan selects unavailable or repeated tracks.

Assistant uses horizontal tabs: **Chats**, **Models for features** and **Manage endpoints**. Enter an endpoint’s provider/address and save it. StationOS checks the connection and discovers its models automatically before saving; failures display an error and preserve the existing configuration. Then assign providers and discovered models to each feature; manual IDs remain available. Discovery does not generate or download models.

The producer assistant starts catalogue exploration with measured summaries (counts, genre/artist coverage and known duration), rather than sending the library to the model. Targeted recording searches default to five results, capped at 20. A successful summary releases older detailed pages from working context. Local conversations compact completed tool results against the selected endpoint’s context budget while keeping human requirements and the approved plan. The output setting is a maximum, rather than space reserved regardless of the request: StationOS fits each assistant reply to the available context and preserves committed action IDs. Confirmed output-limit interruptions receive bounded continuation in smaller steps, without executing partial tool calls or replaying failed network requests. Producers can describe the desired station naturally and continue an existing saved conversation; no special prompt constraints or manually split tasks are required.

### Find UPnP music servers automatically

Open **Sources → Add source**. StationOS searches its local network and lists verified music servers. **Use server** fills the name and description address; review them and select **Add source**. Discovery does not save or import anything. Servers already configured are labelled **Already added**. If search is unavailable or finds nothing, enter the description URL manually. Without JavaScript, use **Search for music servers** and the same selection form.

Search uses IPv4 SSDP multicast, as defined by the [UPnP device architecture](https://upnp.org/specs/arch/UPnP-arch-DeviceArchitecture-v1.1.pdf). The server running StationOS must be able to send multicast searches and receive UDP replies on the music server’s network. The default Docker bridge may not carry LAN SSDP traffic; manually entered URLs continue to work.

For Linux Docker hosts, `compose.discovery.yaml` is an optional host-network override used alongside `compose.yaml`. It removes published port mappings and retains the same named data volume. The override uses `!reset` and needs Docker Compose 2.24.4 or later. See [Docker host networking](https://docs.docker.com/engine/network/drivers/host/) and [Compose merge rules](https://docs.docker.com/reference/compose-file/merge/). Host networking is opt-in. IPv6 discovery and discovery across routed networks are not implemented.

### Persistent deployment settings

Keep installation-specific settings in the project directory’s **`.env`** file, which Git ignores. Use `.env.example` as a reference for new installations; on an existing installation, edit the existing `.env` and preserve its password and other settings. Do not edit tracked Compose files to store an address. The same repository can then be deployed to another server with different `.env` values.

| Setting | Purpose |
| --- | --- |
| `COMPOSE_FILE` | `compose.yaml` for standard Docker networking; `compose.yaml:compose.discovery.yaml` for Linux LAN discovery. Saving this selects the correct files for future ordinary Compose commands. |
| `STATIONOS_LISTEN_ADDRESS` | In discovery mode, the StationOS host’s address and port. Defaults to `127.0.0.1:8080`. Use its LAN address when the proxy connects from another machine or network namespace. The health check uses the same address. |
| `STATIONOS_PUBLIC_URL` | The public HTTPS origin, such as `https://radio.example.com`, without a path. Used in both networking modes for proxied writes and advertised links. |
| `STATIONOS_ADMIN_PASSWORD` | Your existing management password. Blank disables login. |

For example, a Linux StationOS host with a separate reverse proxy could have these networking entries in `.env` (replace the example address and hostname with the deployment’s actual values):

```dotenv
COMPOSE_FILE=compose.yaml:compose.discovery.yaml
STATIONOS_LISTEN_ADDRESS=192.168.1.20:8080
STATIONOS_PUBLIC_URL=https://radio.example.com
```

`127.0.0.1` is only reachable within the StationOS host’s network namespace. A proxy on a separate server/VM/container must use a reachable address, and StationOS must listen on that address. A loopback-only listener in host mode can cause a proxy using the LAN address to return 502. In standard bridge mode, StationOS continues to listen on the container’s port 8080 and publishes it to host loopback; `STATIONOS_LISTEN_ADDRESS` is specific to the discovery override.

Run Compose from the same project directory. With `COMPOSE_FILE` saved, the normal update command remains `sudo docker compose up --build -d`; no repeated `-f` flags or shell-only environment settings are needed. Explicit `-f` flags override `COMPOSE_FILE`, so avoid selecting only `compose.yaml` on a discovery-enabled deployment. Keep the project name/directory stable to retain the existing named volume. Ordinary pulls and rebuilds preserve `.env`; deploy tools that replace the whole directory must preserve or recreate `.env` and retain the volume.

When upgrading an installation whose `compose.discovery.yaml` was edited locally, first record its actual address and public origin in `.env`. Then restore only that tracked Compose file before pulling the updated parameterised version. Do not discard unrelated changes or overwrite `.env`. Keep `.env` private and back it up separately from Git.

### Producer transmission history

Open a station's **Timeline** or **Incidents** tab; current and upcoming audio appear on **Overview**. The station overview
and producer cards show active incidents and recording-health warnings only when attention is needed.
Timeline records shared server emission once per item, regardless of listener count,
with music/speech details and actual outcomes. It begins after this version is installed;
no older playback is reconstructed from repeat-prevention data. Times use the station's
timezone and UTC offset. Filter by date, type, outcome, show, DJ or literal text, then
export up to 1,000 matching records as CSV. Narrow the filters for larger histories.

Overview shows the actual current and selected next track in compact rows with album
artwork, artists, albums and durations, alongside the next scheduled programme. Opening these views never starts audio or AI. Incident acknowledgement means
seen, not fixed; recovery is recorded separately. New history offers a refresh link without
moving the producer's reading position.

History is saved outside the audio delivery loop. A recording failure or full queue shows
an incomplete-history warning while audio continues. A restart marks unfinished records
as unknown rather than claiming completion. Checkpoints save byte progress approximately every 15 seconds; hard crashes can also lose queued writes. These records do
not prove what a listener heard. History currently has no automatic expiry; monitor database
size and include it in normal backups. Only one server should own a StationOS database.


Broadcast history uses a compact table of recording/activity, show, presenter, playlist,
duration and outcome. DJ spoken text can be expanded; recording cells stay focused on title, artist and album. Internal IDs,
byte counters and generic selection messages stay out of the producer view and CSV.
New records capture the playlist name at transmission; older records may show a clearly
labelled current name, or Not recorded if unavailable. Recurring editions of the same
named show share one filter across weekdays. Update notices follow the active filters
and historical page, ignore ongoing byte checkpoints and report meaningful changes such
as completion rather than treating preparation status as a new broadcast.


History results sit in one card with one pagination toolbar: matching range/total,
first/previous/next/last pages, page jump, 25/50/100 rows per page and CSV export.
Filters are preserved between pages. Paging anchors to the newest matching record
when browsing so later broadcasts do not shift older pages. Applying filters again
starts a fresh view; exports include the complete filtered history within the 1,000-row
export limit rather than just the visible page.

Live screen updates use event subscriptions throughout. Contributor contracts and regression requirements are documented in [docs/live-updates.md](docs/live-updates.md); periodic screen polling is prohibited.
