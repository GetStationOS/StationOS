CREATE TABLE station_announcements (
 id TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 dj_id TEXT REFERENCES djs(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL,
 payload TEXT NOT NULL
);
CREATE INDEX station_announcements_station ON station_announcements(station_id);
CREATE TABLE station_announcement_settings (
 station_id TEXT PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
 dj_id TEXT REFERENCES djs(id) ON DELETE RESTRICT,
 gap_minutes INTEGER NOT NULL DEFAULT 10 CHECK(gap_minutes BETWEEN 1 AND 1440),
 version INTEGER NOT NULL
);
CREATE TABLE announcement_receipts (
 id TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 announcement_id TEXT NOT NULL,
 started_at INTEGER NOT NULL,
 outcome TEXT NOT NULL,
 payload TEXT NOT NULL
);
CREATE INDEX announcement_receipts_station ON announcement_receipts(station_id,started_at);
CREATE INDEX announcement_receipts_message ON announcement_receipts(announcement_id,started_at);
