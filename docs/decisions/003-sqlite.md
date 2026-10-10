# Use SQLite and explicit migrations

Status: accepted · 9 October 2026

## Context

A self-hosted station needs durable configuration and reproducible metadata.

## Decision

Use modernc SQLite with WAL, foreign keys, transactions and embedded immutable numbered SQL migrations with checksums. Keep authoritative stations/sources separate from derived catalogue rows.

## Consequences

One connection serialises work initially. Rebuilds preserve configuration. A future migration must include upgrade fixtures.
