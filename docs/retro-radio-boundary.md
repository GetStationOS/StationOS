# Retro Radio inspection and extraction boundary

Inspected 9 October 2026 before implementation, at Retro Radio commit `b6574f304937f8a0c0ecf48826a4d36d5f7a33a1`. Retro Radio was read only; no migration or deletion of its code or data is proposed.

## Evidence

Reviewed `go.mod`, `docs/architecture.md`, `docs/development-status.md`, `internal/content/content.go`, `internal/upnp/{provider,xml,transport}.go`, and `internal/store/store.go`; inventoried the web, discovery, delivery and test packages. Retro Radio uses Go 1.23, modernc SQLite, server-rendered HTML and vendored HTMX. Its working MinimServer path uses ContentDirectory Browse, paginated DIDL metadata and scoped HTTP delivery. It deliberately does not maintain a persistent music index. The inspected status document reports physical MP3 and converted FLAC acceptance; this is evidence for the patterns, not StationOS hardware acceptance.

## Remains in Retro Radio

Frontier XML and numeric station aliases, receiver capabilities, favourites and device assignments, radio attribution, buffered receiver status, discovery helper deployment, podcast listening, radio directory aggregation and receiver-specific conversion. Its `Station` means a receivable channel; StationOS's station means an operated broadcast service. These models cannot be reused interchangeably.

## Belongs conceptually in StationOS

Station identity and policy; catalogue ownership; deterministic programming and programme logs; broadcast asset production; presenters; playout and distribution. No complete station scheduling/production engine was identified in the inspected tree. Do not present Retro Radio's on-demand receiver relay as continuous station playout.

## Selective reuse

Reuse the DNS-pinned, proxy-free UPnP transport as a small independent adapter implementation, retaining its GPL notice and documenting provenance. Reuse the vendored HTMX asset with its Zero-Clause BSD licence. Use the SQLite driver version already exercised by Retro Radio. Reimplement the SOAP/DIDL adapter against StationOS's source contract: no receiver imports, playback tokens, FFmpeg decisions or device models. Preserve retryable initialization, embedded-device service lookup, resource alternatives, XML limits and server-scoped URL validation. No shared Go library or runtime dependency on Retro Radio.

## Foundation architecture

`UPnP adapter → source contract → catalogue sync → SQLite derived index → catalogue search → server-rendered UI`.

Station and source configuration are authoritative. Imported metadata is disposable. Source sync fetches a bounded snapshot before a single atomic replacement; failure preserves the previous catalogue. Identity is derived from source ID + provider object ID and survives successful rebuilds. Source configuration revisions prevent an old sync overwriting a changed source. Rebuild never deletes stations or source settings.

The requested foundation ends here. Programme logs, programming, production, presenters, outputs and deterministic playout receive documented boundaries, not speculative empty packages. The later music-to-stream milestone must introduce an audio execution/output mechanism and explicit scheduling data before claiming ON AIR.


## Playback follow-up inspection (9 October 2026)

Reviewed internal/upnp/transcode.go, internal/delivery/{delivery,live_buffer,now_playing}.go and internal/web/{audio,listen}.go at the same commit. Reused the checked-byte FFmpeg conversion pattern, pipe-only input, 128 kbps/44.1 kHz/stereo profile, process cancellation, delayed success headers, HEAD/range rules, bounded buffers and rolling write deadlines. Retained GPL-3.0-only provenance on the adapted encoder. Retro Radio opens a relay/conversion per receiver; StationOS instead introduces one listener-driven producer per operated station with shared fan-out. Receiver attribution, finite buffered-duration estimates, Frontier tokens and capability preference logic stay in Retro Radio. No Retro Radio files were modified.
