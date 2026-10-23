# Shared AI prompts

Assistant → Prompts lists the shared instructions for the producer assistant, track selection, show introductions and sign-offs, between-track speech, fictional character guidance, listener contributions, audience moderation and artwork generation. Open a prompt to edit and save it or restore the shipped default. Individual DJ editorial prompts, backgrounds and voice direction remain on their DJ profiles.

Fresh installations and upgraded databases use the existing instructions unchanged until edited. Overrides are operator configuration in `ai_prompts`, independent of the rebuildable catalogue. Defaults are embedded from `internal/prompts/defaults`. Restore default removes the override. Database backups include overrides. A failed prompt read fails the request instead of silently using different instructions.

Changes apply on new AI requests; an active assistant run retains its initial prompt. Prepared show audio includes the complete show and background instructions in its cache identity, so subsequent preparation uses updated text. Already prepared or playing segments finish normally. Provider response schemas, candidate validation, tools, request limits and dynamic runtime context remain enforced by code.

## DJ speech requests

The shipped link, show, character and listener-link defaults are concise. Saved
custom prompts are preserved; use Restore default to adopt the new wording.
Speech adds only the current task's playback and JSON requirements. Normal links
receive current/next recording metadata, DJ personality/background, show direction,
episode theme/notes and up to six recent aired remarks. Handover requests omit
recording metadata and normal transition rules. Audience data/rules are added only
when approved contributions are present. Multi-message sections allow 75 words;
other links and each show introduction/sign-off allow 45 words.

Speech does not receive queue/exclusion IDs, full aired music history, planning
transcripts or previous generation drafts. It uses an independent request rather
than replaying the planning conversation. JSON response schemas, skip handling,
required handovers and audience acknowledgement validation remain enforced.

Track selection and producer-assistant planning retain their existing context and
safeguards. Logo and portrait defaults are unchanged. The assistant's runtime text
now correctly distinguishes show planning from separately configured DJ writing.

On the first recording after a broadcast starts or restarts, speech receives the
episode's theme/notes without remarks aired before that start. Stored episode
history and music rotation remain intact. Later links retain recent aired remarks
for wording variety; current/next metadata alone defines the immediate transition.
