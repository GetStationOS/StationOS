# StationOS contributor guidance

StationOS is independent of Retro Radio. Follow the domain/adapters boundaries in docs/architecture.md.

For any operator-facing feature or UI change, read and follow docs/user-experience.md. Stations are the top-level workspace; Sources are shared; browse tracks within playlist editing. Use dedicated creation pages, task-specific playlist tabs and one pagination row. Reuse the CSS components and HTMX shell, preserve URLs/history/form fallback and station context, and display only measured runtime state.

Playlists are unordered eligibility pools for station curation; never introduce playback ordering into playlist membership. Stable browsing order is not a queue.

Keep operator configuration and playlist choices separate from the rebuildable catalogue. Migrations are immutable after release; add historical upgrade fixtures when changing the schema. Verify meaningful changes with make check and the appropriate tests. Exercise HTMX behaviours in a real browser when navigation or forms change.
