ALTER TABLE station_shows ADD COLUMN music_direction TEXT NOT NULL DEFAULT '';
ALTER TABLE station_shows ADD COLUMN rotation_format TEXT NOT NULL DEFAULT 'varied';
CREATE TABLE show_episodes (
 episode_key TEXT PRIMARY KEY,
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 show_id TEXT NOT NULL REFERENCES station_shows(id) ON DELETE CASCADE,
 starts_at INTEGER NOT NULL,
 payload TEXT NOT NULL
);
CREATE INDEX show_episodes_station_start ON show_episodes(station_id, starts_at);
