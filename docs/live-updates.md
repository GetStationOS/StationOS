# Live UI subscriptions

All background UI updates use server-sent events (SSE). Do not add HTMX `every`
triggers, `setInterval`, recurring fetch timers, or polling fallbacks. Ordinary
links/forms and GET fragment routes remain available without JavaScript; manual
navigation is the fallback when EventSource is unavailable.

## Producers and data ownership

- Broadcast runtime and committed configuration changes use `broadcast.Manager.WatchChanges`.
  Existing web writes and Assistant registry actions call `Changed` after success.
- `changefeed.Bus` is explicitly composed in `cmd/stationos`, then passed to web and
  persistence adapters. Operations, Audience and Assistant writes publish their
  respective topic only after a successful commit/statement, including worker writes.
  Never publish before commit: a reader could otherwise observe the old snapshot
  and miss the change. Never notify from a read-only snapshot handler.
- Assistant activity/usage and recording health have their own runtime observers.
  Queue overflow and history write failure announce the latched unhealthy state.
- Notifications are bounded, coalescing invalidations. Writers never wait for
  network delivery; subscribers re-read the authoritative state. No audio subscription
  is created and no listener count, AI work or broadcast is started by a live view.

## HTTP contract

The existing allowlisted read-only fragment/version routes support `?subscribe=1`:
Overview `/live`, Audience `/audience`, Assistant `/{conversation}/messages`, and
`/operations/indicator` or `/operations/version`. They retain authentication, station,
conversation and filter validation. Invalid requests fail before opening the stream.
The shared adapter in `internal/web/subscriptions.go` subscribes before its initial
read, renders through the existing buffered handler, hashes the response and sends
JSON-encoded snapshot strings only when the rendered content/fingerprint changes.
Filters, pagination and anchors use exactly the normal GET contract.

Existing station directory, player and public programme SSE routes continue to send
their named server-rendered events. All live views share the server's bounded observer
capacity, per-write deadlines and request cancellation. Application shutdown cancels
HTTP request contexts. Reverse proxies must allow streaming; responses send
`X-Accel-Buffering: no`. Fifteen-second comment heartbeats keep connections alive;
heartbeats never read the database, regenerate a view or trigger a browser refresh.

Time passing is not a reason to poll. Audience uses its next actual expiry deadline;
public programmes use their next show start/end or local midnight. These are one-shot
timers recalculated after the boundary or a real data change. Changes made by another
process directly to SQLite are not supported live: use StationOS application services
or reload after maintenance. This remains a single-process runtime.

## Browser ownership and user experience

`data-live-url` declares a region's ordinary snapshot route. The shared client owns
one EventSource per region ID and URL. HTMX navigation closes obsolete streams;
swapping a region does not accumulate connections. Late events must match the current
region URL. Page hide closes streams; page show reconnects. EventSource handles
transport retry, and every reconnect receives the latest snapshot rather than requiring
an unbounded replay log. Do not replace this with timed fetch retry loops.

Overview and directory cards patch changed nodes, retaining unchanged covers and
focused controls. Assistant updates patch only messages/status and preserve the composer,
open details and reading position. Audience retains horizontal scroll and defers the
latest snapshot while a control in its region has focus; it applies after focus leaves.
Historical Timeline/Incident rows stay still: their filtered fingerprint subscription
only enables/highlights the explicit refresh icon. Refresh changes the results card
and baseline, retaining filters and navigation. Disconnection feedback stays local;
never add a global Working/Loading banner.

## Required verification

Run `make check`, `make race` and `make build`. `TestLiveUIHasNoPollingFallback` guards
the embedded templates and application script against recurring polling. Tests cover
committed incident/recovery updates, filtered fingerprints, ignored byte checkpoints,
reconnect snapshots, authentication, invalid filters, cancellation, expiry deadlines,
and coalescing notifications. Add notification tests when introducing another writer.
In a browser verify navigation/back, reconnect, focused controls, mobile/tablet layouts,
and that subscriptions neither start playback nor produce repeated loading messages.
