# Chatterbox Nano speech provider

Chatterbox is an external HTTP service. StationOS never imports or runs its model.
Existing installations continue using their saved OpenAI speech selection until an
operator explicitly changes Speech under Assistant → Models.

## Architecture and configuration

`production.Speech` supplies source audio independently of DJ text writing.
The inference router reads the existing saved Speech connection. OpenAI delegates
to the original production speech transport, retaining model, voice, direction,
speed and retry behaviour. Chatterbox uses `production/chatterbox` to POST
`{"text":"..."}` to the connection's base URL plus `/tts`, adding a server-owned `voice` ID when selected on the DJ. There is no provider
fallback. Existing music-continuity behaviour remains responsible for failed or
late DJ links.

Connections store provider type `chatterbox`, address and response deadline using
the existing AI endpoint settings and SQLite connection table. Migration 37 widens
the provider constraint without changing existing routes or credentials. The UI
offers `http://127.0.0.1:8000` as a local starting address; enter a different base
URL to use a LAN service or, later, `http://chatterbox:8000`. Localhost is always
relative to the StationOS process. Do not append `/tts` or `/health`.

Voice choices come from `GET /voices` on the selected Chatterbox connection.
The response must provide `default_voice` and a nonempty `voices` array of unique,
stable `id`/`name` pairs. StationOS bounds the catalogue to 200 voices and 256 KiB
and validates IDs, names and that the default exists. Discovery has a ten-second
budget, does not synthesize audio and never passes provider credentials.

On a DJ's create/edit page, choose **Chatterbox voice**. **Server default** omits
`voice` from `/tts`, preserving the server's default behaviour. A named selection
sends its exact advertised ID. Choices are saved per speech connection by migration
38 in the DJ's `speech_voices` JSON column. OpenAI's existing `voice` column and
voice/vibe/speed values remain intact. Switching providers restores the appropriate
choice; switching between Chatterbox servers keeps their choices separate.
New or changed selections must exist in the active connection's catalogue.
Unavailable saved voices stay selected and explicitly labelled, allowing unrelated
edits or a reset to Server default. Failed discovery shows an inline warning without
losing the saved choice. A provider switch while editing rejects stale voice saves.
Unknown voices during synthesis produce the server's error; no other voice is
silently substituted. Voice assets and reference recordings stay on Chatterbox.

The assistant's `list_voices` tool reads the same selected-provider catalogue.
For Chatterbox, `save_dj` accepts `speech_voice` with `connection` and `voice` fields;
empty voice selects Server default. Its existing `voice` field still edits the
OpenAI voice. Omitted selections are preserved during ordinary and portrait edits.

The Speech model picker still discovers `chatterbox-nano` through `GET /health`.
This health check uses existing endpoint discovery and advertises no text/tool
capabilities. Token budgets are irrelevant
to Nano. The default response deadline is 180 seconds, configurable from 15 to 600
seconds; connection establishment is limited to five seconds. Production's parent
budget allows sequential show intro/outro requests and normalization, but playback
cancellation still cancels stale work.

Chatterbox WAV bytes stay unchanged until the existing FFmpeg normalizer. The
broadcast fan-out, cached presentation assets and mixer expect 128 kbps, 44.1 kHz,
stereo MP3; WAV cannot be concatenated directly into that MP3 connection. The same
normalization, silence trimming and MP3 output path handles both providers, with a
forced WAV input demuxer for Chatterbox. Input is bounded to 16 MiB for WAV, while
normalised output retains the existing 1 MiB limit. Response media type, RIFF size,
format and nonempty audio chunks are checked before FFmpeg decoding. Errors identify
Chatterbox HTTP, transport, timeout or malformed-audio failures. Optional diagnostic
headers are logged with generation timing, without speech text or credentials.

Existing show-presentation asset storage remains unchanged; its identity now
includes the speech connection so a switch cannot reuse another provider's intro.
This adds no Chatterbox cache, queue or worker service.

## Automated verification

Run `make check`, `make race` and `make build`. Normal tests use mock HTTP servers
and never require a real Chatterbox service. Coverage includes request JSON, exact
WAV preservation, base URL prefixes and trailing slashes, HTTP errors, empty,
malformed, truncated and oversized responses, media type, connection refusal,
cancellation, timeout, redirects, health discovery, keyless readiness, migration
preservation, speech-only routing, retained OpenAI voice options, and WAV conversion
to decodable MP3.

## Real service test without changing a station

From the StationOS repository, with Chatterbox already running:

```sh
STATIONOS_CHATTERBOX_TEST_URL=http://127.0.0.1:8000 \
STATIONOS_CHATTERBOX_TEST_OUTPUT=/tmp/stationos-dj-link.mp3 \
go test ./internal/production/openai -run '^TestChatterboxLiveDJLink$' -count=1 -v
```

The opt-in test first discovers `/voices`, selects the advertised default voice
explicitly (override with `STATIONOS_CHATTERBOX_TEST_VOICE`), and supplies a fixed DJ sentence to the normal Link → speech router →
Chatterbox → FFmpeg path. It avoids paid text inference, checks the result decodes,
and writes the MP3 that broadcasting would receive. Play `/tmp/stationos-dj-link.mp3`
with your audio player. On macOS: `afplay /tmp/stationos-dj-link.mp3`.

## Full station listening test

1. In the updated StationOS build, open Assistant → Endpoints → Add endpoint.
   Name it Chatterbox Nano, choose Chatterbox Nano (speech), and set the reachable
   base URL. Leave the credential blank. Save; discovery should list chatterbox-nano.
2. Open Assistant → Models. Change only Speech provider to that endpoint and choose
   chatterbox-nano. Save models. Keep the existing curation/text provider configured.
3. Open the scheduled DJ’s profile. Choose a discovered Chatterbox voice or leave
   Server default, then save. Choose a station with playable music and a currently scheduled show with a DJ.
   Press Listen. The first listener starts production; the next prepared intro or
   between-track link uses Nano. Music and speech use the same player connection.
4. Listen for the DJ and inspect Timeline for the actual aired speech. Check logs
   for “Chatterbox speech generated” and optional duration/generation/RTF fields.
5. To return to OpenAI, select its Speech endpoint and the desired OpenAI speech
   model in Assistant → Models, then save. DJ voice/vibe/speed values are preserved.

Do not restart or stop an existing server solely to perform these steps. A new
build must be started through the operator's normal update process, or tested on
another port with a separate database.

## Limits to address before caching or deployment

Speech provider selection is global; each DJ retains its OpenAI voice and separate
Chatterbox choices for each connection. Speed and vibe remain OpenAI capabilities.
The legacy `DJ.HasKey` field currently represents speech configuration readiness,
including keyless Nano; it is not proof of provider health. Speech failures retain
the normal music-continuity path, without choosing another speech provider.
The real-service test proves synthesis and playable normalised output; actual
station airtime also depends on its text provider, schedule and accessible music.
Container networking, authentication, deployment and additional caching remain out
of scope. Serialized Nano generation across several stations may consume their
existing response budgets; measure that before choosing queue or worker changes.

## Files changed in this integration

- `cmd/stationos/main.go`: wire speech routing and diagnostic logger.
- `internal/ai/ai.go`: accept Chatterbox connections.
- `internal/inference/router.go`, `speech.go`: select speech providers and budgets.
- `internal/inference/check.go`, `chatterbox_test.go`: reuse health discovery.
- `internal/production/production.go`: speech generator/configuration port.
- `internal/production/chatterbox/chatterbox.go`, `chatterbox_test.go`: bounded HTTP
  WAV provider and mock-server coverage.
- `internal/production/openai/openai.go`, `chatterbox_test.go`: route normal DJ
  speech while preserving OpenAI; mock and opt-in real-service integration tests.
- `internal/production/ffmpeg.go`, `ffmpeg_test.go`: WAV input through existing MP3
  normalizer, with decodability coverage.
- `internal/persistence/ai.go`, `ai_test.go`: speech-only selection and keyless
  configuration readiness.
- `migrations/037_chatterbox.sql`: widen provider types while preserving data.
- `internal/persistence/testdata/v36.sql`, `upgrades_test.go`: frozen prior-schema
  upgrade coverage, including preserved provider credentials/deadlines.
- `internal/web/templates/assistant.html`, `static/app.js`, `ai_test.go`: endpoint
  choice, speech route, default address/model and provider capability guidance.
- `docs/chatterbox.md`: architecture, configuration, verification and manual tests.

## Observed real-service result

The opt-in normal-Link-path test passed against the local CPU Nano service:
5.88 seconds of returned WAV, 2.83 seconds generation, reported RTF 0.48. Existing
FFmpeg normalization produced a decodable MP3 DJ link. A separate temporary
StationOS database was used to verify creating the endpoint, discovering Nano,
saving Speech selection and retaining OpenAI in the browser. No running production
server or saved production routes were changed. The test sentence is fixed, so this
verification does not claim a live show has aired or that a paid text provider was
exercised.

Final verification in the StationOS checkout: `make check` (all tests, vet and
format check), `make race` (all packages), `make build`, and `git diff --check`
passed. The final OpenAI timeout/cache compatibility changes also passed targeted
race tests for inference, DJ production and the application command. Automated
OpenAI regression coverage passed; no paid live OpenAI request was made.


Voice-catalogue follow-up: tests cover catalogue decoding/limits/errors, optional
voice request JSON, retained OpenAI choices, independent server selections,
unavailable choices, stale forms, assistant tools, schedule projections, and selected
voices through both links and show presentation. The real LAN service advertised
`nano-default`; explicitly selecting it produced 5.48 seconds of WAV in 2.48 seconds
(RTF 0.45), followed by successful normal MP3 conversion and decoding. Multiple
named voices are verified with mock servers; the current real catalogue has one.

Final verification of the voice-catalogue follow-up in the StationOS checkout:
`make check`, `make race`, `make build` and `git diff --check` all passed. Browser
verification used a separate database: the selected Nano voice survived saving and
reopening, with the DJ's original OpenAI `coral` value preserved independently.
The temporary server was stopped after verification; the running user server and
its provider settings were not changed.
