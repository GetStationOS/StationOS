# 009: Listener-driven shared station broadcasts

Accepted 9 October 2026.

Stations assign one of their unordered playlist pools to a stable, editable stream address. First-listener admission creates a single producer; subsequent listeners share that programme; zero listeners cancels source and codec work. Playback selection is a temporary random boundary, independent of membership, until a curator supplies an explicit programme.

Use one Go worker and at most one FFmpeg process per active station, with bounded listener queues and process admission. This concentrates audio conversion cost at the station rather than the listener. It does not provide a multi-process StationOS cluster: each station runtime belongs to one process. Stream bytes are common 128 kbps MP3; delivery is paced centrally. The encoder gets checked source bytes through pipes and cannot resolve remote manifests or source URLs.

Store playlist assignment and unique slug with operator configuration. Existing station addresses derive from names during migration 5; automatic collision suffixes preserve uniqueness. Renaming leaves addresses stable; explicit changes invalidate old device presets. Management authentication remains separate from public GET/HEAD streams for external devices.

The app player lives outside HTMX main/history swaps and pauses by releasing its subscription. This preserves navigation and avoids treating a paused browser as a listener. No silent autoplay, seek bar or playlist ordering is introduced. Short track-boundary gaps, browser buffering, a single assigned pool and temporary shuffle are accepted limits of this milestone.
