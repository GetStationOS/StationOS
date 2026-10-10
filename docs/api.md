# HTTP interfaces

The management UI uses ordinary HTML form routes with CSRF tokens; these are internal UI interfaces, not the public API.

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/healthz` | Process liveness: 200 `{"status":"ok"}` |
| GET | `/readyz` | DB accessible: 200 `{"status":"ready"}`; 503 `{"status":"unavailable"}` |
| GET | `/api/v1/status` | 200 `{"status":"ready","playout":"listener_driven","catalogue_items":0}` |

The status API uses management Basic authentication when configured. Health/readiness endpoints remain unauthenticated. Status errors are JSON `{ "error": { "code": "unavailable", "message": "database unavailable" } }` (503), or `internal_error` with a generic message (500). Unsupported methods return 405; unknown paths return 404. These routing errors use net/http's text format; a full public CRUD API and unified routing-error envelope are planned.

`playout: listener_driven` advertises shared station broadcast capability (or `unavailable` for a web server composed without the runtime). Current/next data is measured from delivered bytes. Stable station/source IDs are random 128-bit hexadecimal strings; catalogue IDs are deterministic source-scoped 128-bit hashes. Do not use SQLite numeric row identifiers in future public contracts.

## Management catalogue and playlists

`GET /content` browses the shared catalogue. Parameters: `q`, `genre`, `album_choice` (JSON source_id/name), `source`, `sort` (title/artist/album/duration), `direction` (asc/desc), `per_page` (25/50/100), `page` (one-based), and optional target `playlist`. `GET /playlists?station=<id>` lists a station's playlists; `GET /playlists/<id>` resolves a paginated playlist. Legacy catalogue station parameters no longer restrict source visibility.

Protected POST forms create/rename playlists at `/playlists`, add catalogue selections at `/playlists/select`, and add/remove rules, exclude tracks at `/playlists/<id>/rules`, `/rules/<rule>/remove`, `/remove`. These remain internal HTML interfaces. Genre/album matches are dynamic; no public CRUD API is introduced.

## Station workspaces and batch selections

`GET /` lists station cards. `GET /stations/new` creates a station; `GET /stations/<id>` is its overview; `GET /stations/<id>/settings` edits its identity/settings. Existing `POST /station` continues to create/edit and redirects to that workspace. `/station` remains a creation-form compatibility route.

`POST /playlists/<id>/selections` replaces all genre/album rules atomically using repeated `genre` and `album` fields. An empty submitted set clears the rules without changing manual choices or exclusions. Existing missing albums may remain selected; newly added albums must exist in the catalogue. Limits remain 100 rules and 10,000 resolved entries. Form bodies are bounded at 1 MiB, including encoded album tokens.

HTMX GETs return a titled fragment; normal and history-restore GETs return the complete shell. Successful HTMX writes return `HX-Location` targeting `#main`, while normal writes return 303. Known notice codes supply short success messages. Runtime status comes from the shared producer and active listener subscriptions.

Playlist membership is an unordered eligibility pool. There is no move/reorder endpoint; future curation/programming will own broadcast sequencing.

## Station streams

- `GET /streams/<address>` joins the station's live 128 kbps MP3 stream. It starts an idle station only when its assigned playlist can play. Additional listeners share the same producer and join the live point.
- `HEAD /streams/<address>` advertises audio/mpeg and Accept-Ranges:none without admitting a listener or starting an encoder. Deleted/unknown addresses return 404. GET startup or capacity failures return 503 before audio headers; the connection closes on failures after playback begins.
- The app adds an opaque random 128-bit `listener` query parameter per listening session. A second request with the same station/session replaces its previous connection atomically, preventing browser media probes from counting twice or consuming another listener slot. Different sessions and external connections without this parameter remain independent. This is connection identity, not authentication; do not reuse an app session URL on another device.
- Live streams ignore Range and return 200 at the live point, including Safari’s initial `bytes=0-1` probe. No seeking is advertised. They omit Content-Length, disable caching/proxy buffering and have rolling write deadlines.
- GET/HEAD streams do not require management authentication. Stream addresses are stable stored station slugs, never resource URLs. Unknown management paths remain protected.
- Protected `POST /stations/<id>/broadcast` assigns a station-owned `playlist` or clears assignment with an empty value. Changing assignment ends the old broadcast.
- Protected `POST /station` accepts `stream_slug` for editing the address; lowercase ASCII letters, digits and single hyphens, maximum 80 characters. A duplicate custom address returns 422. A rename preserves the current address unless a new one is submitted.
- Protected `GET /stations/<id>/live` supplies the overview status fragment; `/stations/live` supplies current homepage cards. Polling neither starts audio nor replaces editing forms.

The app's player is outside `#main` and the HTMX history element. Explicit Listen starts playback; Pause closes the connection and Resume reconnects at the current live point. Full reloads close the player. Stream URLs use `-public-url` when configured, otherwise the request's actual scheme/host; forwarded headers are not trusted.


## DJ configuration

Protected management routes: GET `/djs`, `/djs/new`, `/djs/{id}` and `/djs/{id}/delete`; POST `/djs` saves, POST `/djs/{id}/delete` requires `confirm=yes`. All writes use the existing CSRF and same-origin policy. Successful saves/deletes return normal 303 or HTMX HX-Location to the DJ list. Validation retains HTMX form input; ordinary forms redisplay non-secret fields.

Fields: `id`, `name`, `model`, `voice`, `prompt`, `vibe`, write-only `api_key`, and optional `clear_key=yes`. Blank key keeps the existing key; clear and replace together are rejected. `model=custom` uses `custom_model`. Presets verified against the [official OpenAI model catalogue](https://developers.openai.com/api/docs/models) on 9 October 2026: gpt-6.1-sol, gpt-6-luna and gpt-6-astra. Model access depends on the operator’s API account; saving settings does not validate access or call OpenAI. Prompts/vibes are bounded to 32/16 KiB. The profile HTML never contains saved credentials. Keys are encrypted in SQLite with a private `<database>.secrets-key` sidecar; that file is required when restoring encrypted profiles. No DJ public JSON or generation endpoint is exposed yet.

Voice choices use the [13 built-in OpenAI text-to-speech voices](https://developers.openai.com/api/docs/guides/text-to-speech#voice-options), with Cedar selected initially. The voice and vibe are separate settings; the future speech adapter must use a compatible speech model such as gpt-4o-mini-tts. Unsupported voices are rejected without changing the saved profile.
