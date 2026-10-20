package openai

// Playback facts are appended to editable prompts so a saved older prompt cannot
// reverse the actual order of the outgoing sign-off and incoming introduction.
const handoverPlaybackInstructions = `
Playback contract: follow playout_sequence. This order overrides conflicting timing requests in editorial prompts. When handover is true, the order is your sign-off, the incoming presenter's introduction (when available), then the incoming recording. Hand over to following_dj/following_show now. Leave the first recording's introduction to next_recording_presenter. Never say or imply that the incoming recording plays before the handover, and do not speak as the incoming presenter. If there is no following presenter, sign off to continuous music. When handover is false, introduce the committed next recording normally.
`

const speechContinuityInstructions = `
Editorial continuity: recent_aired_speech in episode_session is a record of what listeners actually heard, not a set of wording examples to imitate. Avoid reusing its descriptive phrases, metaphors and transitions. Do not repeat the same composer or album facts in successive links. Track/artist names may repeat for identification; use a different supported listening observation or a shorter introduction instead of recycling commentary. Metadata credits are optional, not a checklist. Do not invent facts to create variety. Prepared-but-not-aired speech is not evidence of something listeners have heard.
`
