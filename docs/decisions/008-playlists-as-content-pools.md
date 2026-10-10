# ADR 008: playlists are unordered content pools

Accepted, 9 October 2026. Supersedes the saved-order portion of ADR 007.

## Context

The operator wants a human/AI curator to select the radio stream from station playlists, following the Retro Radio approach. A fixed playlist sequence duplicates that responsibility and misleadingly suggests playback priority.

## Decision

A playlist defines eligible tracks through dynamic genre/album rules and explicit inclusions/exclusions. It has no editable or persisted playback sequence. Stable title/ID sorting supports browsing and pagination only. The Tracks tab exposes exclusions and addition, without row sequence numbers or move controls.

Migration 4 drops playlist_order, retaining stations, playlists, rules, snapshots and exclusions. Previous migrations remain immutable. Historical version 3 upgrade tests verify membership preservation.

## Consequences

Future curation consumes this candidate pool and station policy to choose music. Its ordered output belongs to a programme log or queue, separate from eligibility. This change does not implement the AI curator or audio playout.
