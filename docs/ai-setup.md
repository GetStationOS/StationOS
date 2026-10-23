# AI setup

Use AI to help configure your station, select eligible music and give fictional presenters a voice. Choose the services that suit your installation; paid services charge through your own accounts.

## Connect a service

Open **Assistant → Manage endpoints** in the producer workspace. Add a named endpoint with its provider, address and credentials where required. Saving checks connectivity and discovers available models. Discovery does not generate speech, download models or start listening.

Shared credentials are encrypted and never displayed. A blank credential field retains the saved value. Back up the database and its `.secrets-key` together.

The endpoint must be reachable from StationOS. Inside Docker, `localhost` means the container. Use a reachable private address for services on another computer.

## Choose models for text and artwork

Under **Models for features**, choose the endpoint and model for the production assistant, show planning, DJ writing and audience review. DJs share the selected services; their individual editorial briefs give them different personalities.

Show planning and DJ writing have separate endpoint and model choices. Show planning selects music; OpenAI `gpt-6-luna` uses the Decisions API to choose one eligible recording from a shortlist. DJ writing generates introductions, spoken links and sign-offs. DJ writing inherits the previous combined setting on upgrade. OpenAI Show planning switches to `gpt-6-luna`; other planning providers retain their model. Changing either role leaves the other unchanged. Other planning models/providers retain structured text selection.

Text connections support OpenAI, native Ollama, OpenAI-compatible chat services, native Anthropic and native Gemini. Compatibility depends on the model’s ability to follow the required tool and structured-response contracts. A successful connection check alone does not establish programme quality.

Artwork generation uses the configured OpenAI service. The assistant can create station logos and fictional DJ portraits; image generation is separately billable. Existing artwork stays in place unless a replacement is requested, and a failed image does not undo creation of the station or DJ.

## Give each DJ a voice

Speech is selected on the DJ profile. Different DJs can use different speech endpoints, models and voices.

| Service | Voice setup |
| --- | --- |
| OpenAI | Choose a speech model, voice, speed and voice direction. |
| Chatterbox | Choose a model and voice advertised by your own reachable speech server. |
| ElevenLabs | Choose an available speech model and a saved voice from your account. |

Provider-specific setup: [Chatterbox](chatterbox.md) · [ElevenLabs](elevenlabs.md).

Changing endpoint retains its saved DJ preferences. Missing voices are labelled unavailable; check the service before replacing a saved selection.

## Use the production assistant

Open **Assistant** globally or within a station. Describe the result you want in ordinary language, such as a station identity, playlist brief or weekly schedule.

The assistant can make saved changes to sources, stations, playlists, DJs, schedules, mixer settings and audience decisions. Its action records link to the results. Review them as you would changes made through the forms.

Chats survive navigation and restart. Use **Continue saved request** for an interrupted request, or **Stop assistant** to cancel active work. Completed changes remain saved if a later step fails.

## Plan for running costs and delays

Text, speech and image generation may incur separate charges. Local services need their own hardware and model installation; StationOS does not install or download those models.

Endpoint response deadlines and context limits bound requests. Slow or unavailable AI can delay curation or speech while eligible music continues. Each feature uses its selected endpoint and model; failures do not silently switch to a different cloud service.

[All guides](README.md)

### Ollama thinking

Assistant → Endpoints → your Ollama endpoint has a Thinking switch, off by default
for new and upgraded endpoints. Save the endpoint to apply it to subsequent text
requests in every role. StationOS explicitly sends `think: false` or `think: true`
to Ollama; terminal chat settings do not affect it. The selected model must support
the chosen setting. Other providers do not receive this Ollama-specific field.
