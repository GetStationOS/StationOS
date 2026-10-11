ALTER TABLE stations ADD COLUMN repeat_minutes INTEGER NOT NULL DEFAULT 180 CHECK(repeat_minutes BETWEEN 1 AND 10080);

-- Exact content identities survive catalogue rebuilds; only station deletion cascades.
CREATE TABLE station_track_plays (
    station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    track_id TEXT NOT NULL,
    last_started_at INTEGER NOT NULL,
    PRIMARY KEY(station_id, track_id)
);
CREATE INDEX station_track_plays_recent ON station_track_plays(station_id, last_started_at DESC, track_id);
