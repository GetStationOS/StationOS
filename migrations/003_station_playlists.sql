CREATE TABLE playlists (
 id TEXT PRIMARY KEY, station_id TEXT NOT NULL REFERENCES stations(id),
 name TEXT NOT NULL, description TEXT NOT NULL
);
CREATE INDEX playlists_station ON playlists(station_id);
-- Rules and manual selections are authoritative and never reference disposable catalogue rows.
CREATE TABLE playlist_rules (
 id TEXT PRIMARY KEY, playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('genre','album')), value TEXT NOT NULL, source_id TEXT NOT NULL,
 UNIQUE(playlist_id,kind,value,source_id)
);
CREATE TABLE playlist_selections (
 playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
 content_id TEXT NOT NULL, included INTEGER NOT NULL CHECK(included IN (0,1)),
 snapshot TEXT NOT NULL CHECK(json_valid(snapshot)), PRIMARY KEY(playlist_id,content_id)
);
CREATE TABLE playlist_order (
 playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
 content_id TEXT NOT NULL, position INTEGER NOT NULL CHECK(position>0),
 PRIMARY KEY(playlist_id,content_id), UNIQUE(playlist_id,position)
);
