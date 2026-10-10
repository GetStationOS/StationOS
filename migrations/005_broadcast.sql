ALTER TABLE stations ADD COLUMN stream_slug TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX station_stream_slug ON stations(stream_slug) WHERE stream_slug<>'';
CREATE TABLE station_broadcast (
  station_id TEXT PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
  playlist_id TEXT REFERENCES playlists(id) ON DELETE SET NULL
);
