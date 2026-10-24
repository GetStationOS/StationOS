# Producer tools designed for agent clarity

Adopted 24 October 2026. This is an internal producer tool interface, not an
externally exposed MCP server.

See the [complete producer tool reference](producer-tools.md) for every advertised
tool, its inputs, runtime requirements, feedback and compatibility-only calls.

The agent chooses music and writes editorial copy. The server validates IDs,
selections, scheduling arithmetic, conflicts and persistence. Completing a request
must not require understanding an internal work-step state machine.

## Interface contract

- Offer the same capabilities on every turn. Recovery, selected work area and read
  counts must not hide catalogue, playlist or scheduling tools. A context helper
  may add useful saved facts; it is never a prerequisite or permission gate.
- Prefer flat task inputs to nested domain patches. Required fields and examples
  belong in schemas and concise tool descriptions.
- Return saved IDs and facts sufficient to verify the action. Eligibility counts
  are measured; a committed write with a failed readback remains successful and
  reports the uncertainty. A name alone never selects music.
- Invalid calls explain the correction and whether anything was saved. Internal
  protocol mistakes are not human-input blockers.
- Keep the original brief authoritative. An intentional schedule gap or unrelated
  empty pool does not create a new requirement. Verification can finish without
  another mutation. Never silently mark a checklist complete.
- Preserve bounded no-progress, repeated unchanged-write and provider-error stops.
  Stable capabilities do not imply automatic retries of uncertain writes.

## Implemented task tools

`create_playlist(station_id, name, genres, description?)` creates a new pool and
its initial genre rules in one transaction through the playlist service and
repository. Genres are alternatives; Jazz and Funk match either genre. Exact
catalogue names are preferred; unique case-insensitive matches are canonicalized.
The result includes playlist_id, station_id, name, saved genres and measured
available/unavailable counts. Unknown future genre values remain legal and may
produce an empty pool with explicit repair feedback. Existing selection tools
support albums, artists, composers, dates and individual recordings.

`create_show(station_id, name, dj_id, playlist_id, days, start_time,
duration_minutes, description?, music_direction?)` creates one recurring programme.
The start is a station-local 24-hour clock string, and duration is 15–1440 minutes
in quarter-hour increments. The server calculates midnight and overnight endings.
Days use Monday=0 through Sunday=6. Existing schedule services reject bad references
and overlaps atomically across every requested day. The response includes show_id,
all saved days, clock times, duration, DJ/playlist references and public copy.
`save_show` remains for editing existing fields. The old nested `create_shows`
interface is no longer advertised but is accepted for saved-call compatibility.

`set_station_description` and `set_show_description` change public copy alone and
return saved text. Identical values do not write or count as progress.

`inspect_station(station_id? or station_name?)` resolves a station and returns
measured settings, playlist eligibility and weekly schedule when additional facts
are needed. Existing get/list tools provide details. The old `select_work_step`
call remains accepted for saved-call compatibility but is no longer advertised.

## Example and verification

For “a new Jazz show with Miles, daily 8pm until midnight, using a new Jazz and
funk playlist”: resolve station/Miles and discover catalogue genres; create the
new pool with `["Jazz", "Funk"]`; create one show using its returned playlist_id,
all seven days, `start_time: "20:00"`, `duration_minutes: 240`. Compare the returned
facts with the brief and complete progress. Inspecting existing playlists is not a
prerequisite to an explicitly requested new pool.

Deterministic tests cover this saved configuration, OR eligibility, canonical genre
spelling, midnight/overnight arithmetic, rejected conflicts, failed pool creation,
and stable tool access even after repeated reads and during recovery. Existing
large-history/native-provider tests enforce bounded context and mutation safety.
These tests do not establish Gemma reliability; live Ollama scenarios remain opt-in.

## Further work

Keep simplifying legacy patch tools and evaluating real model transcripts. The
new playlist task covers genre pools; a general selection object should be added
only with equally clear category semantics and transactional tests. Consider
persistent idempotency keys for retry protection across separate runs; current
creation replay protection is within a run. Do not add keyword-based brief routing
or another required planning protocol as a substitute for clear tools.
