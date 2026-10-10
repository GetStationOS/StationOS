# Own the audio source contract

Status: accepted · 9 October 2026

## Context

UPnP is one source among future licensed and non-music providers.

## Decision

Own Browse/Get/Resolve/Profile in sources. Normalize into content.AudioItem; capabilities and restrictions are explicit. UPnP models stay inside the adapter.

## Consequences

Unsupported operations remain visible. Audio URLs never imply permission to broadcast, cache or modify.
