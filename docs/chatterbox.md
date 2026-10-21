# Chatterbox speech in StationOS

Chatterbox is an external HTTP service. StationOS never imports or runs its model.
Each DJ chooses a speech endpoint, model and voice. OpenAI and Chatterbox can be
used by different DJs on the same station; DJ writing and music curation retain
their global text settings.

## Configure a DJ

1. Add a shared connection under Assistant → Endpoints. Choose Chatterbox
   (speech), enter its reachable base URL and leave the credential blank. Saving
   checks `/health`; the service must report ready `chatterbox-nano`.
2. Open the DJ's create/edit screen. In Voice, choose that speech endpoint.
   The form shows models discovered from `GET /models` and voices from `GET /voices`.
   Choose a named voice or Server default, then save the DJ.
3. OpenAI endpoints instead show speech model, voice, conversation speed and vibe.
   Switching endpoints restores saved settings. Unsaved settings also survive
   switching within the form and are saved together with the profile. Previewing
   a provider never writes data, generates speech or creates a listener.

Shared addresses and credentials stay under Assistant → Endpoints. Speech no
longer appears under the global Models settings. New DJs default to the built-in
OpenAI endpoint and gpt-4o-mini-tts until another endpoint is chosen. Migration 39
copies the old global speech endpoint/model to every existing DJ, retaining voice,
speed and vibe and all prior Chatterbox voice choices. Normal upgrades do not
change which provider existing DJs use.

Localhost refers to the StationOS process. Use `http://127.0.0.1:8000` only when
both processes run directly on the same host; use a reachable LAN/server address
otherwise. Do not append `/tts`, `/voices` or `/health` to the saved base URL.

## API and production path

`GET /voices` returns `default_voice` plus a nonempty `voices` array of unique,
stable `id`/`name` pairs. StationOS limits discovery to ten seconds, 256 KiB and 200
voices, validating IDs, names and default membership. Reference recordings and
voice assets stay entirely on Chatterbox. Discovery never generates audio.

Generation posts `{"text":"...","voice":"server-owned-id","model":"original"}` to `/tts`.
Server default omits `voice`, preserving the service's default behaviour. Missing
saved voices remain selected and labelled unavailable. Failed discovery retains
the saved choice and permits unrelated edits; new voice choices require successful
validation. Unknown voices during synthesis return an error, without substitution.

The production adapter scopes routing, credentials, identity and deadlines to the
DJ snapshot for each request. Concurrent DJs do not mutate a shared provider
selection. OpenAI delegates to its existing speech transport and retry behaviour.
Each Chatterbox model has the connection's configurable response deadline (180 seconds by default,
15–600 seconds allowed), a five-second connection timeout and normal cancellation
at playback boundaries. Voice/vibe/speed controls are shown only for OpenAI.

WAV remains unchanged until the existing FFmpeg normalizer, which produces the
128 kbps, 44.1 kHz stereo MP3 expected by broadcast fan-out and presentation assets.
Input media type, RIFF structure, bounds and nonempty audio are validated first.
WAV input is limited to 16 MiB; normalized output keeps the existing 1 MiB limit.
Diagnostic headers X-Audio-Duration, X-Generation-Time and X-RTF are optional and
logged with elapsed time. Errors do not trigger another speech provider.

Migration 38 stores prior Chatterbox choices in `speech_voices`. Migration 39 adds
DJ-owned `speech_connection`, `speech_model` and per-endpoint `speech_settings`.
Prepared-audio identity includes the DJ's settings and connection. Active DJ
references prevent endpoint deletion or changing its provider type. Credentials
remain in the existing encrypted shared connection store.

## Assistant tools

`list_speech_endpoints` lists available connections without addresses or credentials.
`list_voices` accepts `dj_id` or `connection` and discovers the appropriate catalogue.
`save_dj` accepts `speech` with `connection` and `settings` (model, voice, speed,
vibe). Omitted speech settings survive editorial and portrait edits. The legacy
OpenAI `voice` and Chatterbox `speech_voice` tool fields remain compatible.

## Verification

Run `make check`, `make race` and `make build`. Normal tests use mock servers and
require no Chatterbox process. They cover API bounds/errors, optional voice JSON,
migration from historical schemas and configured OpenAI/Chatterbox installations,
per-DJ readiness, endpoint retention, provider switching, offline choices, unsaved
form state, normal-form fallback and concurrent DJ generation through different
providers and credentials. OpenAI regression calls use mock transports.

For a real service, from the StationOS repository:

```sh
STATIONOS_CHATTERBOX_TEST_URL=http://192.168.1.116:8000 \
STATIONOS_CHATTERBOX_TEST_VOICE=paul \
STATIONOS_CHATTERBOX_TEST_OUTPUT=/tmp/stationos-dj-link.mp3 \
go test ./internal/production/openai -run '^TestChatterboxLiveDJLink$' -count=1 -v
```

Omit STATIONOS_CHATTERBOX_TEST_VOICE to use the advertised default. The opt-in test
supplies a fixed sentence through the normal DJ Link → speech router → Chatterbox
→ FFmpeg path, checks decoding and saves a playable MP3. It makes no paid text call.
Play that file in an audio player; on macOS, `afplay /tmp/stationos-dj-link.mp3`.

For actual airtime, use a scheduled DJ configured as above on a station with
playable music and working text inference. Listen starts the station. The next
prepared introduction/link uses that DJ's provider, in the same MP3 connection as
music. Check Timeline for actual aired speech. Updating settings does not start a
broadcast. Do not stop an existing server solely for this test; use the operator's
normal update process or an isolated instance with a separate database.

The real-service test passed with the advertised Paul voice: 5.96 seconds of WAV,
2.32 seconds reported generation, RTF 0.39, followed by normal MP3 conversion and
successful decoding. Browser tests used an isolated database; the user's running
server and production configuration were not changed. This verifies synthesis and
playable output, not that a scheduled live show has aired.

Final verification: `make check`, `make race`, `make build` and `git diff --check`
passed. Final targeted race checks covered concurrent provider routing, assistant
endpoint edits, complete preference restoration and normal/HTMX provider previews.
In an isolated browser instance, Alex used Chatterbox/Paul while the other DJs
retained OpenAI. Switching restored tts-1/Marin preferences, previews left Save
unsaved, and selected settings survived saving and reopening. The temporary
instance was stopped without restarting or modifying the user's running server.


## Multiple Chatterbox models

`GET /models` returns `default_model` and a nonempty `models` array with unique
`id` and readable `name` values. Each DJ selects its own model. Nano and Original
appear when advertised; Turbo and future models appear automatically. Discovery
does not load models or generate audio. The request uses the exact selected ID in
`model`. There is no automatic model fallback on synthesis failure.

Existing `chatterbox-nano` settings map to `nano` at the UI and request boundaries;
saving writes the canonical ID. Cached audio and history are never relabelled.
Model choices survive endpoint switching. Removed selections are retained and
labelled unavailable; catalogue failure permits unrelated edits but blocks a new
model choice until it can be validated. A legacy server returning 404 for `/models`
can still advertise Nano through its ready `/health` response.


### Model-advertised inline speech cues

Chatterbox GET /models may supply capabilities.inline_cues as tag/name pairs.
StationOS validates bounded unique tags and names, then passes only the selected
DJ endpoint/model's list to the speech-writing generator alongside the shared
producer prompts and DJ personality. No model-name allowlist is used. Missing,
empty or unavailable capability data disables cues; normal speech can continue.
OpenAI and ElevenLabs do not inherit Chatterbox cues. Discovery does not generate
speech or run in a background polling loop.

For the explicit cue test, every nonempty link/introduction/sign-off must have
exactly one advertised cue. This overrides the earlier sparse-cue policy. The
generator must choose and write the sound, varying its choice and placement.
StationOS never inserts a missing cue, so omissions remain visible when testing
the prompt. Valid existing cues retain their original placement.
Unknown/excess complete tags are stripped before synthesis; malformed brackets
and cue-only utterances are rejected. Normal optional-link silence, response
formats, editorial facts, handovers and approved audience requirements remain.
The final synthesized text, including accepted tags, is the timeline transcript.
Capability-dependent delivery instructions participate in prepared-audio identity
so changed capabilities invalidate prior prepared speech.
