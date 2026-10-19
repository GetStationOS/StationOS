# Episode planning

Each dated occurrence owns a durable editorial session. Recurring shows supply an optional musical-direction prompt; DJ profiles supply personality and presentation. A session never carries editorial conversation into another dated occurrence. Revisions invalidate affected plans while retaining producer decisions for review.

Episodes prepare automatically before airtime without starting audio, opening a stream or requiring producer approval. A validated plan is ready to air immediately. Producers may inspect the proposed running order, request revisions and lock or remove recordings. Explicit producer choices take precedence over subsequent autonomous replanning.

The episode session can search the complete eligible playlist through bounded tools. Results contain recording metadata and durations, never source URLs or credentials. All choices are validated server-side against current station sources, playlist membership and rotation policy. A varied mix is the default. Artist and album specials are requested in the DJ music prompt; the agent records its reason for deliberate repetition in the episode plan. There is no separate rotation-format setting.

Keep persisted session conversation and tool outcomes together with the episode plan, producer changes and actual aired recordings. Resume that session after listener dormancy and process restarts. Record actual playback only after the first audio bytes are emitted. Queued, failed and skipped recordings must not be described as aired. Bound provider context through an explicit retained episode summary and recent exchanges without silently dropping producer constraints.

Proposed running orders belong to episodes, not unordered playlist membership. Duration estimates reserve time for speech and remain estimates. Revalidate planned selections at playback boundaries; adapt future choices to requests, changed membership, failures and remaining time. Prepare speech close to airtime for the confirmed next recording. If planning or AI fails, local rotation continues without blocking unattended broadcasting.

Show the selected dated week's occurrences on the schedule. Pink means unplanned or no usable plan; amber means preparing or for review; green means validated and ready. Always include text labels. Readiness is independent of broadcasting and optional human review. Open an occurrence to inspect and edit its plan; retain a separate route to recurring show settings.

Background preparation must deduplicate work per episode, use bounded concurrency, survive restart through durable job state and retry with backoff. Publish state changes after database commits through existing live-update subscriptions; do not introduce browser polling. Retire stale work when show settings change or shows are deleted. Planning never changes the listener-driven audio lifecycle.
