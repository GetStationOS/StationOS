# StationOS

StationOS is an independent, modular radio station operating system built with Go, SQLite, server-rendered HTML and HTMX. It manages stations and their playlists against one shared content catalogue. Retro Radio remains a separate receiver/home-audio product.

## Current status

StationOS now provides **listener-driven station streams**. Create and edit stations, configure shared UPnP/MinimServer sources, import metadata, browse the full library and build station-owned playlists from whole genres, albums and individual tracks. Genre and album selections automatically include new matching tracks after a source import. Imports preserve previous data on failure; successful rebuilds retain station, source and playlist configuration. Assign a station playlist, connect to its stream URL or choose Listen in the app. Every listener shares one programme. Weekly shows select their DJ and playlist in station-local time. DJs curate eligible music and prepare spoken links off the playback path; a music fallback keeps broadcasts running when preparation is unavailable.

## Build and run

Requires Go 1.23 or later and FFmpeg on PATH (`brew install ffmpeg` on macOS; included in Docker). SQLite needs no native library or CGO. Initial dependency download requires network access; the normal tests need only local resources and loopback HTTP listeners.

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

Playlist selections form a union: overlapping choices include a track once. Removing a track creates an explicit exclusion, including from genre/album rules; adding it again removes that exclusion. Playlists are unordered pools of eligible music. The DJ curator chooses the broadcast sequence; alphabetical title sorting is only for browsing. Rules, manual choices and exclusions survive catalogue rebuilds. Missing manually selected items retain their saved metadata and show as unavailable. Albums are identified by source and album title; identically titled editions within one source currently share a selection. Genres match the exact imported value (for example, `Jazz` and `jazz piano` are separate choices).

**Upgrading an existing installation:** stop the old process, then run `make run` (or rebuild and start the executable). Startup applies migrations through version 9 automatically, preserving station/source IDs and catalogue data while making sources shared. Back up the database first; migration 1 is unchanged. Do not run the old executable against an upgraded database.

Imports copy metadata and resource addresses only; audio stays on the UPnP server. MinimServer imports use its flat all-items view, avoiding repeated virtual artist/genre/date menus.

Sources must resolve to a trusted server address. Loopback, multicast, link-local and unspecified addresses are rejected. DNS is pinned during each import, resources and redirects remain on that server, and proxy environment variables are ignored. Rebuild to pick up address changes. Playback freshly resolves each source object through the same checked transport. Only checked audio bytes reach FFmpeg; no music files are copied into the database.

The UI uses HTMX for navigation and writes within a stable application shell, with real URLs, back/forward history, save feedback, loading/error states and ordinary-form fallback. Station overview polls measured runtime state every three seconds. Homepage cards and the persistent player use live events and update only changed content; editing forms remain stable. The player shows current and next titles and artists as you navigate.

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

Open http://127.0.0.1:8080. When `STATIONOS_ADMIN_PASSWORD` is set to a non-empty value, sign in as `admin` with that password. If the variable is unset or empty, authentication is disabled and the management UI and API are accessible without login. Compose publishes to host loopback and retains `/data` in a named volume. The runtime runs as a non-root user. Container health checks use readiness. UPnP access uses outbound HTTP to the configured server; SSDP and host networking are not required by this milestone. Docker Desktop/container networking must be able to reach that server.

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

Choose **DJs → Create DJ** to save a fictional name, AI model, voice, prompt and voice-direction vibe. **Use example vibe** inserts the sample voice/phrasing/punctuation/tone directions. Model presets follow the [OpenAI model catalogue](https://developers.openai.com/api/docs/models); **Other model…** accepts a model ID available to your API account. The model is for writing and curation. Speech uses `gpt-4o-mini-tts` with the selected voice, vibe and conversation speed. The speed slider ranges from 0.25× to 4.00×, defaults to 1.00× and affects newly generated introductions, sign-offs and track links. Saving profiles makes no API requests; listening to a scheduled show with a configured key starts background OpenAI preparation and may incur API charges.

API keys are optional while configuring profiles. Saved keys are encrypted and never displayed: a blank edit keeps the current key; enter another to replace it or select **Remove saved key**. Keep the database and its `.secrets-key` file together when backing up/restoring. The sidecar is generated automatically with owner-only permissions on server startup and must remain private. Encryption does not protect against access to both files or the running server. Startup refuses to create a replacement sidecar if encrypted keys already exist. Profiles are preserved when rebuilding the catalogue.

## Weekly schedule and DJ curation

In a station, choose **Schedule → Add show**. Select a weekday, start/end times in 15-minute steps, a shared DJ, and one of that station’s playlists. Shows repeat weekly in the station’s timezone; an end time at or before the start finishes the following day. Overlapping shows are rejected, including Sunday-to-Monday overlaps. Use the scrollable weekly calendar or **Show list** to edit a slot. A DJ or playlist used by a show must be replaced in that show before it can be deleted.

Three editable starter DJs are added once: **Alex Bright** (upbeat Pop, Coral voice), **Miles Mercer** (relaxed, informed Jazz, Onyx voice), and **Clara Vale** (warm, thoughtful Classical, Sage voice). Each has a curation/chatter prompt and voice-direction vibe. Keys are blank; add a key in the DJ profile to enable AI curation and speech. Upgrades preserve existing profiles; restarting preserves edits and deletions of the defaults.

The first listener activates the current show. Music continues from the station’s default playlist during gaps; assign a playable default to keep these gaps covered. The current track finishes before handover, and the same open MP3 connection carries the outgoing presenter, next show and music for every listener. A show that ended while a long track played is skipped. Schedule edits take effect at track boundaries. Stopping the last listener cancels production and fetching.

The DJ uses its editorial prompt and musical knowledge to curate a bounded set of up to 64 eligible tracks from up to 256 sampled candidates, with title, artist, album, genre and duration. These choices are a transient show plan, separate from unordered playlist membership. Near the end, local duration planning uses known lengths and short combinations to reduce overrun; unknown lengths and decoder delays prevent an exact timing guarantee. Ready spoken audio is included in the remaining-time budget. Between tracks, the DJ receives the current song, queued next song and handover target; source URLs and API keys are not included in that editorial input.

Responses and speech are prepared in the background, bounded by time/response-size limits. Saved plans and normalized intro/sign-off audio are cached per occurrence/profile and handover, with at most 64 cached records (each audio segment at most 1 MiB). Per-track links are prepared while music plays and are used only if ready and still relevant. Missing keys, failed requests or late assets produce music-only fallback; the API cannot hold up a timing decision. Speech and music share 128 kbps, 44.1 kHz stereo MP3 framing. Source-opening latency and MP3 encoder padding can still cause brief audible gaps; crossfades and sample-perfect gapless mixing are not implemented.

Clock-injected tests verify finishing tracks, sharing handovers without reconnecting, weekly/default resolution, overlap handling and daylight-saving boundaries. Repeated autumn boundaries use their first occurrence, so adjacent shows share a boundary; missing spring times advance to the next valid minute and collapsed shows are omitted. API tests use fake responses and do not spend API credit. A durable execution audit and full deterministic programme log remain future work.

## Producer station cards

The homepage now shows each station’s active programme, DJ, local show times and playlist; stations without a running show show the configured default playlist. Current and next tracks include artist, album, duration and artwork where available. Cards also show measured listener count, DJ preparation/failure or missing-key state, finish-track/handover status and the following show. Dormant stations remain clearly empty. DJ and playlist names link to their settings; Listen uses the persistent player.

Cards poll quietly without stealing keyboard focus, and card navigation opens the full station workspace. Artwork is served through StationOS from the source’s checked transport, with an eight-second timeout, a 4 MiB raster-image limit, four concurrent requests and private browser caching. Missing artwork uses a music placeholder. No database migration is required for this UI change.


## Stream buffering and hardware radios

The server defaults to `-stream-buffer 5s` (configurable from `0s` to `5s`). The first listener receives a bounded startup burst, followed by 128 kbps delivery. Later listeners receive approximately the most recent five seconds from the same programme, then join ongoing delivery atomically. Short supply stalls retain their catch-up budget so the device can rebuild its reserve. Device buffering determines audible latency; current/next metadata reflects server delivery.

FFmpeg output is read ahead through a bounded eight-second encoded buffer for the current and queued tracks. This absorbs source/encoder jitter without fetching or storing whole songs. Recent audio is shared per station; slow listeners remain bounded and cannot block others. Extended source outages or a persistently slow network can still exhaust buffers. Logs report `Audio supply stalled` and `Slow stream listener disconnected` for diagnosis.

For Nginx Proxy Manager, leave response caching disabled for the station proxy. StationOS sends `X-Accel-Buffering: no` on streams and live events. If custom proxy settings override this, pass through `/streams/`, `/radio-events`, `/radio/...` and `/producer/stations/...` event responses without buffering or compression. To isolate device/network/proxy faults, compare the same station's HTTPS stream with its direct LAN HTTP stream. The management password is not needed for either stream URL.


### Public radio and producer access

The public homepage is `/`; management is `/producer`. Individual listener pages use `/radio/<stream-address>`, and the existing MP3 feeds stay at `/streams/<stream-address>`. Older management bookmarks redirect to their new producer addresses. Use `STATIONOS_ADMIN_PASSWORD` on an internet-facing deployment and keep `-public-url` set to the public HTTPS origin; leaving the password empty retains the optional unauthenticated management behaviour.

Station Settings → Public visibility controls publication: Listed appears in the directory, Unlisted works through direct page/stream links, and Private is available only through producer preview. Existing and new stations default to Unlisted, so an upgrade does not automatically list stations or break existing public stream URLs. Changing a station name preserves its addresses; changing Stream address deliberately changes both page and feed. Public pages include live player metadata, weekly programmes and a copyable internet-radio URL. Opening a page never starts production.
