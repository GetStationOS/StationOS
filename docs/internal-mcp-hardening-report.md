# Producer assistant: internal MCP hardening report

23 October 2026

## Assessment

Gemma4:12b can complete the simpler station-configuration scenarios after the
interface changes below. It is **not yet reliable enough for unattended creation
of a more elaborate weekly station**. The largest scenario saved the requested
configuration but failed to finish its verification checklist within 12 minutes.
That remains a failed evaluation, rather than a successful station-creation run.

StationOS currently exposes an internal capability registry to the producer runner,
not a network MCP server. This work hardens that registry, its model-facing schemas,
results and recovery protocol. It does not introduce an external execution endpoint.

## Evaluation setup

The live tests use the real producer runner, native Ollama adapter, registry,
application services and SQLite repositories. Each scenario receives a fresh
in-memory database and an imported synthetic catalogue containing 24 Jazz and
24 Soul recordings, including durations, artists, albums and distinct identities.

The selected model was `gemma4:12b` on the configured Ollama-compatible development
endpoint. Settings were 16,384 context tokens, a 2,048-token reply ceiling, thinking
off and a 180-second provider deadline. Each scenario has a 12-minute outer limit.

Only inference reaches the model server. Tests do not open the operator database,
fetch music, generate artwork or speech, or start broadcasting. The existing
StationOS server, credentials and configuration were left alone. No preview server
was started. The development changes have not been applied by restarting the server.

## Latest results by scenario

These are individual runs during the hardening iteration, not a statistical success
rate or a single clean full-suite run of the final revision.

| Scenario | Result | Model requests | Elapsed | Observed outcome |
| --- | --- | ---: | ---: | --- |
| Daily Jazz, one presenter, all seven days 00:00–24:00 | Pass | 9 | 108 s | Persisted identity, default, 24-recording pool, presenter, music direction and seven daily editions matched the brief. |
| Weekdays 08:00–18:00; weekends 10:00–14:00 | Pass | 11 | 193 s | Both recurring programmes, exact hours and reuse of one pool/presenter passed the independent assertions. |
| Full Jazz/Soul week, two presenters, four programmes | **Fail: scenario timeout** | 26 | 720 s | Recorded readbacks showed all four requested programmes and their correct assignments/hours. Both pools had 24 available recordings. The agent kept verification in its remaining checklist and did not finish. |

The final mixed-week run exercised the new empty-response recovery against the
real model. It preserved earlier writes, recovered from an explicitly completed
empty response, and subsequently created the missing Weekend Jazz programme.
A malformed call containing a placeholder ID and nested fields for the wrong tool
was rejected before mutation; the model then corrected it.

Readbacks in rounds 20 and 22 showed weekday Jazz 00:00–12:00, weekday Soul
12:00–24:00, weekend Soul 00:00–12:00 and weekend Jazz 12:00–24:00, with the
requested presenters and nonempty music directions. The checkpoint still retained
“Verify full 7-day schedule” after those reads. The test timed out before its final
independent assertion block, so this evidence does not turn the run into a pass.

## Findings and implemented hardening

### 1. Successful writes were mistaken for usable music

Initially, a lowercase `jazz` rule saved against catalogue value `Jazz` selected no
recordings. Gemma nevertheless announced completion. Another run created pools
with genre names but added no rules at all, again treating them as populated.

Implemented:

- New-pool results explicitly state that the pool starts with zero eligible
  recordings and that its name does not select music.
- Rule edits, track selection changes and default assignment return measured
  total, unavailable and available counts. Empty results include a repair hint.
- Available counts also appear in durable summaries, surviving ordinary context
  reduction.
- If a count read fails after a committed write, the result preserves success and
  reports unverified eligibility. It neither invents zero nor advises repeating
  the write.
- Unique case-insensitive genre, artist and composer matches resolve to actual
  catalogue spelling and report both requested and canonical values. Exact,
  ambiguous and unknown values are preserved; album/source scope is unchanged.

This keeps exact matching in the domain while removing an observed model-facing
casing trap. Unknown future rule values remain possible configuration.

### 2. Show-creation errors did not identify the repair location

Gemma repeatedly moved `station_id` between the top level and the nested `show`
object. A generic unknown-field error did not break that loop.

The creation schema and description now explicitly locate `show.station_id`.
Edit-only ID and weekday fields are absent from the creation object's offered
schema. Misplaced or missing station IDs receive location-specific correction
messages. Decoder errors continue to state that the failed call saved no shows
and earlier successful calls remain committed. Atomic schedule validation remains
unchanged.

### 3. Discovery and playlist kinds used confusing vocabularies

Gemma sent singular `genre` to discovery, which requires plural `genres`.
Discovery now advertises a required enum of supported facets. Playlist-rule kinds
retain their separate singular enum. Regression coverage checks that every
advertised discovery facet is actually accepted by the dispatcher.

### 4. A completed empty reply aborted recoverable work

The endpoint sometimes returned a successful, explicitly completed response with
no usable text or tool calls. Previously this became a generic failure, losing the
reply's usage information.

The adapter now returns a typed empty-response error with usage retained and no
executable output. The runner permits one continuation with compact context and
focused work-area tools, preserving the brief and committed actions. Repeated empty
completions still pause. Uncertain transport failures remain non-retryable; private
thinking is never interpreted as a tool call.

Deterministic tests cover the bound, discarded output, usage and continuation
without recreating a committed station. The mixed-week live run demonstrated the
continuation path, although the complete scenario subsequently timed out.

### 5. Prompt contracts described obsolete behaviour

The shipped assistant prompt described separate records for each weekday. It now
matches the current recurring-programme model: one programme owns its complete
weekday selection; editing days replaces that selection; deletion archives the
whole recurring programme and retains history.

The prompt also requests exact catalogue values, honest treatment of empty pools
and returned relative URLs. Earlier output invented a hostname; the evaluation
now rejects invented absolute link hosts. This is prompt guidance and test
detection, not a new runtime URL-validation gate. Saved operator prompt overrides
are preserved and do not automatically acquire the revised default text.

## Remaining problems

**Completion remains unreliable for the larger request.** Gemma repeated reads and
saved an unchanged verification checklist despite having the required state in
front of it. The next interface change should make verification machine-readable:
expected windows and assignments, measured differences, and an explicit completion
result. A natural-language remaining item is currently too easy to leave pending.
Do not solve this by silently clearing the checklist or accepting an unverified
success message.

**Long recovery exchanges are expensive.** Several later requests took roughly
40–60 seconds, compared with many early tool calls at 2–12 seconds. Context
reconstruction and prompt-prefix reuse deserve investigation; the timings alone
do not establish the cause. The 12-minute failure was the scenario limit cancelling
the active request, not proof that one provider request exhausted its separate
180-second allowance.

**Future catalogue behaviour is not covered by current membership assertions.**
The mixed run proposed additional adjacent genres such as blues, funk and R&B that
were absent from the fixture catalogue. Those rules can broaden membership after
future imports. The next scenarios should test that deliberate future rules are
distinguished from unverified additions to the producer's brief.

Further useful cases include editing an existing station without affecting another,
interrupted-run continuation, real overlap repair, empty/unavailable libraries,
ambiguous catalogue labels, overnight programmes and amendments to an approved
brief. Existing deterministic scheduling tests already cover midnight and atomic
conflicts, but those behaviours have not all been evaluated with the live model.
Speech, artwork, musical editorial quality and actual transmission are outside this
configuration evaluation.

## Verification and artifacts

`make check` and `make build` passed. The full `make race` run passed; the producer
and inference race tests were also rerun successfully after the last production
feedback change. Preview-only scaffolding was removed after the report-only request;
producer and inference tests still pass. No database snapshot or preview instance
was created.

The new opt-in suite is `TestOllamaStationScenarios`, available through
`make test-ollama`. Ordinary checks skip live inference unless its endpoint variable
is explicitly set. See [the testing guide](producer-ollama-testing.md) for commands,
assertions, budgets and transcript format.

Local diagnostic artifacts from this session:

- `/tmp/stationos-gemma-verified/daily.jsonl`
- `/tmp/stationos-gemma-verified/weekend.jsonl`
- `/tmp/stationos-gemma-mixed-final/mixed_week.jsonl`

Each transcript contains offered definitions, portable inputs, native text/tool-call
fields before adapter validation, parsed results, timings and usage. HTTP headers,
credentials and model-private thinking are excluded. Files contain synthetic data
and remain outside Git.

The working tree contains the tests, interface changes and documentation. The
appropriate current conclusion is **improved and reproducible, with a remaining
complex-task completion failure**; Gemma4:12b has not been established as a reliable
default producer model.

## Follow-up: description and verification loops — 24 October 2026

Operator examples exposed failures when adding public descriptions and when checking
an already-saved Saturday 06:00–20:00 programme. The latter recorded successful
creation but retained verification as remaining work, then paused after focused
recovery. These examples are not evidence that description persistence itself failed.

Inspection found that recovery selected a scheduling/playlist work area from any
measured schedule gap or empty pool, even without evidence that the original brief
required those hours or that pool. This could direct verification of a deliberately
partial schedule back towards configuration work. Show list/review projections also
omitted the public description.

Implemented follow-up:

- Dedicated description-only tools with existing IDs, required text, explicit
  clearing, domain validation and saved-text results. Other settings are preserved.
- Description previews and truncation flags in show listings and review projections.
- A focused descriptions area without timing mutations, DJ or playlist discovery.
- No-op results for identical descriptions, with no write, revision advance or
  configuration event. Three repeated identical no-op edits pause early.
- Recovery no longer infers required work from gaps or unrelated empty pools.
  It offers explicit area selection and read-only review, with instructions to
  complete verified checklist items and keep internal protocol issues out of
  human-input blockers.

Deterministic regressions exercise description updates/clearing, malformed and
cross-station edits, retained timing/presenter/pool/music direction/recurrence slots,
no-op revision preservation, early loop termination, and read-only completion of
the Saturday show despite intentional gaps and an unrelated empty pool. Existing
24/7 repair regressions still require and verify complete weekly coverage, now
through explicit work-area selection rather than inferred scope.

These follow-up checks do not generate model traffic. A fresh live Gemma run has
not been performed for these changes, to avoid adding inference load while the
operator's Mac was already under heavy load. The original live evaluation results
above remain unchanged; further model evaluation is still required. The running
server and its database have not been modified.

## Agent clarity refactor — 24 October 2026

The reported daily Miles/Jazz/Funk request exposed a capability-access failure:
recovery hid catalogue and playlist tools and removed reads after two uses. The
agent interpreted this as a human blocker. Work-area and read-count restrictions
have been removed. `inspect_station` provides saved facts without choosing a work area; the legacy selector is no longer advertised.

The adopted [interface contract](producer-agent-interface.md) adds transactional
`create_playlist` with initial genres and flat `create_show` with clock start and
duration. Results carry saved IDs, selections, measured eligibility and schedule
facts. Legacy nested show creation is no longer advertised. Invalid calls and
empty pools retain actionable feedback; existing no-progress stops remain.

Verification uses deterministic fixtures and native-provider regressions, without
new Gemma inference or changes to a running server. Live completion-rate and token
comparisons remain to be measured; earlier live scenario results are unchanged.

Validation: `make check` and `make build` passed. Race tests passed for producer,
playlist services, persistence and inference; the producer/inference race tests
were rerun after the final inspection-tool changes. The runner regression finishes
with or without an initial station inspection, requires no work-step selection or
extra final review round, and repeats identical creation without duplicating a show.
The playlist transaction regression rejects an unavailable album after starting
the transaction and confirms that no partial pool remains.
