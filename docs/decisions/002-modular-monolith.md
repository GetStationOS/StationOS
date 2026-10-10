# Use a modular monolith

Status: accepted · 9 October 2026

## Context

The initial deployment must be simple and self-contained but support growth.

## Decision

Use small cohesive domain packages with explicit repository/provider ports, composed by one Go process. HTTP and SQL stay at the edges.

## Consequences

Boundaries can evolve without distributed-service operational costs. Do not create empty speculative packages.
