CREATE TABLE station_shows (
 id TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 weekday INTEGER NOT NULL CHECK(weekday BETWEEN 0 AND 6),
 start_minute INTEGER NOT NULL CHECK(start_minute BETWEEN 0 AND 1439 AND start_minute%15=0),
 end_minute INTEGER NOT NULL CHECK(end_minute BETWEEN 0 AND 1439 AND end_minute%15=0),
 dj_id TEXT NOT NULL REFERENCES djs(id) ON DELETE RESTRICT,
 playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE RESTRICT,
 revision INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX station_show_time ON station_shows(station_id,weekday,start_minute);
CREATE TABLE presenter_assets (
 cache_key TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 plan TEXT NOT NULL,
 intro BLOB NOT NULL,
 outro BLOB NOT NULL,
 created_at INTEGER NOT NULL
);
