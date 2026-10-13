# StationOS contributor guidance

StationOS is independent of Retro Radio. Follow the domain/adapters boundaries in docs/architecture.md.

For any operator-facing feature or UI change, read and follow docs/user-experience.md. Stations are the top-level workspace; Sources are shared; browse tracks within playlist editing. Use dedicated creation pages, task-specific playlist tabs and one pagination row. Reuse the CSS components and HTMX shell, preserve URLs/history/form fallback and station context, and display only measured runtime state.

Playlists are unordered eligibility pools for station curation; never introduce playback ordering into playlist membership. Stable browsing order is not a queue.

Keep operator configuration and playlist choices separate from the rebuildable catalogue. Migrations are immutable after release; add historical upgrade fixtures when changing the schema. Verify meaningful changes with make check and the appropriate tests. Exercise HTMX behaviours in a real browser when navigation or forms change.

Public programme shows remain fully visible as schedule rows. Clicking a show
reveals content underneath; clicking its title again closes it. Use a quiet
plus/minus indicator on the right; do not add a separate show-details close button.
The contribution workflow has its own top-right X to cancel back to its invitation. Avoid native arrow dropdowns or chevrons in this workflow.

Use card outlines to group distinct tasks, rather than every content layer.
Keep day, schedule rows and show information unbordered inside the programme
panel. The active show row and expanded details share a soft tinted background; give contribution choices, forms and confirmation one quiet bordered
container with comfortable padding. Omit the interaction section when the show
has no available participation options; never hide the scheduled show itself.
