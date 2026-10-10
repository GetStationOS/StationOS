# Keep AI outside live playout

Status: accepted · 9 October 2026

## Context

External generation latency and outages cannot control broadcast timing.

## Decision

Production generates and validates assets ahead of time behind text/speech/audio interfaces. Playout consumes ready assets and deterministic fallbacks.

## Consequences

AI may assist preparation, but is never required for a timing decision. Failures degrade presentation rather than blocking broadcast.

## Current adapter

The Responses API prepares bounded eligible-track selections and show intros/sign-offs; speech uses the configured voice and vibe with `gpt-4o-mini-tts`. Per-track links are requested while the track plays and used only if ready at its boundary. The live worker rechecks eligibility and occurrence and uses music fallback instead of awaiting generation. Cached intro/outro plans are separate from execution; links are transient.

Reference: [Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=responses), [Speech API](https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create).
