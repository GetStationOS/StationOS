# Contributing to StationOS

StationOS uses Go, SQLite, server-rendered HTML and HTMX. Start with [AGENTS.md](AGENTS.md), [architecture](docs/architecture.md) and the [interface standards](docs/user-experience.md) before making changes.

## Build and check

Use Go 1.27.2 or later and FFmpeg on PATH. Docker includes FFmpeg. SQLite needs no native library or CGO. Dependencies may need an initial network download.

```sh
make build
./bin/stationos
```

Open `/producer` on the local server. The default database is `data/stationos.db`; templates, styles, timezone data and migrations are embedded.

Run checks appropriate to the change:

```sh
make check
make race
make build
```

Verify interface changes in a browser, including ordinary forms, navigation/history, keyboard access and mobile layouts. Tests use local fakes rather than paid AI requests.

## Keep changes focused

Preserve domain/adaptor boundaries. Keep SQL in persistence, use application services for mutations and make migrations immutable after release. Playlists remain unordered eligibility pools. Live views use event subscriptions; read the live-update contract before changing them.

Keep databases, `.env`, encryption keys and generated runtime files out of Git. Preserve third-party notices and the GPL-3.0-only licence.

## Technical references

- [Architecture](docs/architecture.md) and [architecture decisions](docs/decisions/)
- [HTTP API](docs/api.md)
- [User experience](docs/user-experience.md) and [UI components](docs/ui-components.md)
- [Live updates](docs/live-updates.md)
- [Episode planning](docs/episode-planning.md)
- [AI prompts](docs/ai-prompts.md)
- [Chatterbox integration](docs/chatterbox.md) and [ElevenLabs integration](docs/elevenlabs.md)
- [Starter portrait provenance](docs/dj-portraits.md) and [third-party notices](NOTICE.md)

Explain the problem, resulting behaviour and relevant validation in your pull request. For documentation changes, check links and the described workflow against the current interface.
