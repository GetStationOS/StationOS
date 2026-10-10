# ADR 007: Shared catalogue and station-owned playlists

Status: accepted, 9 October 2026.

## Context

Pop, jazz and classical stations must use the same source library while selecting different content. Duplicating source imports per station would duplicate metadata and couple library configuration to programming choices.

## Decision

Sources and the rebuildable catalogue are global to the installation. Playlists belong to stations and retain genres, albums, individual inclusions/exclusions and saved ordering as authoritative data outside catalogue rows. Genre and album rules automatically pick up newly imported matches, as requested by the operator. Rules combine as a union; explicit exclusions win. Manual inclusions save metadata snapshots so temporary source absence remains visible. Album rules currently identify a source and exact album title; edition-aware album identity is future work.

Migrations preserve existing station/source IDs, revisions and catalogue metadata. Frozen previous-schema fixtures verify upgrades. Playlist resolution is separate from timed programme logs and audio execution.

## Consequences

A library import benefits all stations without erasing their selections. Overlapping rules do not duplicate tracks. Missing rule-only tracks leave the resolved list while the rule remains; missing manually chosen tracks show as unavailable. Bounds of 100 rules and 10,000 resolved entries keep this initial implementation predictable. Future scheduling must snapshot an explicit playlist selection into its own programme log.

The ordering decision is superseded by [ADR 008](008-playlists-as-content-pools.md).
