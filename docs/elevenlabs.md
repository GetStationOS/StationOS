# ElevenLabs speech

Add an endpoint under Assistant → Endpoints, choose ElevenLabs (speech), and use
`https://api.elevenlabs.io` (without `/v1`). Enter an ElevenLabs API key with Models,
Voices and Text to Speech permissions. The key is encrypted like other endpoint
credentials. Blank retains it; it is never stored on a DJ profile.

Save a designed voice to your ElevenLabs account first. On a DJ profile choose the
ElevenLabs endpoint, a speech model and an account voice, then Save. Speech is
created directly by ElevenLabs; no Chatterbox cloning or training is involved.
The default model is `eleven_multilingual_v2`; available speech models are discovered
from the service. Model access and voice access depend on your account.

ElevenLabs applies the voice settings stored in its account. StationOS does not
send OpenAI direction or speed controls to ElevenLabs. Switching endpoints keeps
each endpoint’s saved and unsaved DJ preferences. OpenAI and Chatterbox remain
available. Failed voice discovery retains saved choices; removed voices are labelled
unavailable, and unrelated edits remain possible.

Opening forms and endpoint discovery do not generate paid speech. Broadcasting
uses POST `/v1/text-to-speech/{voice_id}` with `mp3_44100_128`, then the existing
FFmpeg broadcast conversion. Requests respect the endpoint response deadline,
reject redirects, bound response sizes, and redact provider error bodies. Failed
or uncertain generation is not automatically replayed by the adapter.

The model catalogue uses GET `/v1/models` and filters for text-to-speech support.
The voice catalogue uses paginated GET `/v2/voices` with `voice_type=saved` to
show My Voices, bounded to 2,000 voices and a
ten-second discovery deadline. Oversized, incomplete and invalid listings report a
local error. No background discovery polling is added.

API references: https://elevenlabs.io/docs/api-reference/models/list,
https://elevenlabs.io/docs/api-reference/voices/search,
https://elevenlabs.io/docs/api-reference/text-to-speech/convert.
