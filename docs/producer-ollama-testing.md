# Producer assistant: live Ollama scenarios

The producer assistant uses the internal capability registry, not an exposed MCP
server. These scenarios exercise the real conversational runner, registry,
application services, SQLite repositories and native Ollama adapter. Only inference
uses your development server. The tests never open `data/stationos.db`, connect to
an existing StationOS instance, discover/import real sources, fetch audio, generate
artwork or start broadcasting. You can leave the development server running.

## Running

Select an already installed, tool-capable model on an Ollama-compatible endpoint:

```sh
STATIONOS_TEST_OLLAMA_URL=http://localhost:11434 \
STATIONOS_TEST_OLLAMA_MODEL=gemma4:12b \
STATIONOS_TEST_TRANSCRIPTS=/tmp/stationos-gemma-run-1 \
make test-ollama
```

Use your configured network origin instead of localhost when Ollama runs elsewhere.
The model defaults to `gemma4:12b`; nothing downloads or substitutes a model.
The runner uses a 16,384-token context, 2,048-token maximum reply, thinking off,
a 180-second provider deadline and a 12-minute deadline per scenario. Scenarios
run sequentially. Ordinary `make check` and `make race` skip live inference unless
`STATIONOS_TEST_OLLAMA_URL` is explicitly set. The dedicated target requires it.

Run a single scenario while tuning:

```sh
STATIONOS_TEST_OLLAMA_URL=http://localhost:11434 \
STATIONOS_TEST_TRANSCRIPTS=/tmp/stationos-gemma-daily \
go test -v ./internal/producer -run 'TestOllamaStationScenarios/daily$' -count=1 -timeout 15m
```

## Scenarios and assertions

Each scenario starts from a fresh in-memory database and a catalogue imported through
the production repository: 24 Jazz and 24 Soul recordings, with distinct identities,
performers, albums, durations and synthetic resources. Artwork dependencies are
absent and the brief explicitly excludes generation. No audio needs to be served.

| Scenario | Requested configuration |
| --- | --- |
| `daily` | One Jazz presenter, one default pool, one recurring programme, all seven days 00:00–24:00. |
| `weekend` | One shared Jazz pool/presenter; weekdays 08:00–18:00 and weekends 10:00–14:00, with default music in gaps. |
| `mixed_week` | Full 24/7 week; Jazz and Soul pools, two named presenters, four programmes switching genre/presenter between weekday and weekend mornings/afternoons. |

Assertions inspect persisted state independently of the assistant's final answer:
station identity/timezone; exact pool counts and recording membership; Jazz default;
exact recurring-programme and dated-weekday window counts; every requested name,
weekday, time, presenter, genre and nonempty music direction; duplicate windows;
and invented absolute link hosts. An `idle` conversation alone is not success.

These are nondeterministic model evaluations, not replacements for deterministic
regression tests. Repeat a scenario into a new transcript directory to assess
reliability; one passing run does not establish a success rate. Speech readiness,
artwork, source connectivity and real audio transmission are outside this suite.

## Diagnosing results

The dedicated target defaults transcripts to `/tmp/stationos-ollama`; choose a new
directory per comparison to retain earlier runs. Direct `go test` only records
transcripts when `STATIONOS_TEST_TRANSCRIPTS` is set. Each scenario writes JSONL:

- `request`: effective instructions, portable input, offered schemas and options.
- `native_response`: returned text/tool calls, completion status and token counts,
  captured before adapter validation can discard a truncated/invalid response.
- `response`: parsed calls/text, usage, elapsed time and adapter error.
- Final status, checklist and visible messages, including committed tool outcomes.

HTTP headers, credentials and model-private thinking are excluded. Inputs contain
only synthetic fixture data. Files are created with owner-only permissions. Inspect
failed call results in subsequent request inputs; successful writes remain committed
inside that scenario even when later calls fail. Logs from an interrupted run retain
completed request/response events.

## Failures identified during initial Gemma evaluation

- Discovery used singular `genre` instead of the supported plural `genres`.
  Discovery now exposes a required enum, distinct from singular playlist-rule kinds.
- A named playlist was mistaken for a populated pool. Creation now explicitly
  reports zero eligibility and explains that names select no recordings; default
  assignment also returns measured eligibility without rejecting saved empty pools.
- A lowercase `jazz` rule saved successfully against exact catalogue value `Jazz`,
  selected zero recordings, and was reported as a completed station. Unique case-insensitive genre/artist/composer matches now resolve to catalogue
  spelling and report the requested and saved values in `normalizations`; exact,
  ambiguous and unknown values are preserved. Selection writes also return measured available/total/unavailable counts immediately, with
  repair guidance for an empty pool. A failed verification read preserves the
  successful write and reports unknown eligibility, never a fabricated zero.
- Weekday creation repeatedly moved `station_id` between the top level and `show`.
  Creation now describes the nesting explicitly, offers only creation fields,
  and gives location-specific errors for a misplaced or missing `show.station_id`.
- An explicitly completed empty response stopped an otherwise recoverable task.
  The runner now makes one continuation with compact context and focused tools,
  retaining usage and committed work. Repeated empty completions and uncertain
  transport failures still pause; private thinking is never treated as an action.
- The shipped prompt described obsolete independent weekday records. It now
  describes one recurring programme, complete weekday replacement on editing,
  and whole-programme archiving. It also requests exact returned relative links.

Deterministic regressions cover supported discovery facets, measured playlist
feedback and repair, retained writes after verification failure, actionable show-argument errors, ambiguity-safe spelling resolution, and bounded
empty-response continuation without duplicated writes. Existing scheduling tests cover atomic overlaps and midnight.
Saved operator prompt overrides are preserved; the corrected shipped prompt applies
where the default is used (or after an operator explicitly restores that default).
