# Separate StationOS from Retro Radio

Status: accepted · 9 October 2026

## Context

Retro Radio operates receivers and home-audio listening. StationOS operates a broadcast station.

## Decision

Create an independent module and database. Copy only small proven infrastructure with provenance; communicate through published audio/HTTP interfaces.

## Consequences

The products evolve independently. A little infrastructure duplication is accepted; no shared library is introduced.
