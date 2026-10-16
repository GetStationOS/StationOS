# Shared AI prompts

Assistant → Prompts lists the shared instructions for the producer assistant, track selection, show introductions and sign-offs, between-track speech, fictional character guidance, listener contributions, audience moderation and artwork generation. Open a prompt to edit and save it or restore the shipped default. Individual DJ editorial prompts, backgrounds and voice direction remain on their DJ profiles.

Fresh installations and upgraded databases use the existing instructions unchanged until edited. Overrides are operator configuration in `ai_prompts`, independent of the rebuildable catalogue. Defaults are embedded from `internal/prompts/defaults`. Restore default removes the override. Database backups include overrides. A failed prompt read fails the request instead of silently using different instructions.

Changes apply on new AI requests; an active assistant run retains its initial prompt. Prepared show audio includes the complete show and background instructions in its cache identity, so subsequent preparation uses updated text. Already prepared or playing segments finish normally. Provider response schemas, candidate validation, tools, request limits and dynamic runtime context remain enforced by code.
