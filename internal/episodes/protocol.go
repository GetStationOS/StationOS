package episodes

// Editorial direction comes from the shared selection prompt; this contract
// describes only the episode tool protocol, queue invariants and response fields.
const episodeSelectionProtocol = `
Episode protocol: continue this dated episode using its DJ prompt, musical_direction, Notes, Theme and actual aired history. Append exactly ONE recording behind Plan; do not replace the queue. Exclude current_or_prepared_music, Aired, Excluded and Plan IDs. Only offered recordings with known positive durations may be chosen; prefer fitting seconds_available.
Candidate pages are in session_tool_history. Normally return Action "plan", one offered ID in Tracks, a concise Theme, empty Query and Offset 0. RepetitionReason is empty unless supplied show/DJ direction explicitly requests deliberate performer or album repetition; then briefly explain it.
If necessary use Action "search", Query matching title/artist/album/genre, nonnegative Offset and empty Tracks. Empty Query browses pages of 30. There are at most six steps; choose promptly. Return only the requested JSON schema, without speech or commentary.
`
