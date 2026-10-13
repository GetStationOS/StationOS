ALTER TABLE station_shows ADD COLUMN accept_requests INTEGER NOT NULL DEFAULT 0 CHECK(accept_requests IN (0,1));
ALTER TABLE station_shows ADD COLUMN accept_shoutouts INTEGER NOT NULL DEFAULT 0 CHECK(accept_shoutouts IN (0,1));
CREATE TABLE audience_messages (
 id TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 show_id TEXT NOT NULL REFERENCES station_shows(id) ON DELETE CASCADE,
 occurrence_key TEXT NOT NULL,
 show_name TEXT NOT NULL,
 sender_key TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('request','shoutout')),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','review','approved','rejected','used','expired')),
 listener_name TEXT NOT NULL,
 message TEXT NOT NULL,
 track_id TEXT NOT NULL,
 track_title TEXT NOT NULL,
 track_artist TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 show_start INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX audience_station_queue ON audience_messages(station_id,status,created_at);
CREATE INDEX audience_show_queue ON audience_messages(station_id,occurrence_key,status,expires_at);
CREATE INDEX audience_sender_rate ON audience_messages(sender_key,created_at);
