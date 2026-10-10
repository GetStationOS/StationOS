# Separate programming from playout

Status: accepted · 9 October 2026

## Context

Editorial selection and precise execution have different reliability requirements.

## Decision

Future programming produces immutable deterministic programme log events; playout executes and audits them independently. The weekly schedule and cached immutable DJ preparation plans are now implemented separately from listener-driven execution. A durable deterministic event log and execution audit remain future work.

## Consequences

Changes to selection policy cannot silently alter an executing log. Tests must use deterministic clocks and execution fakes.
