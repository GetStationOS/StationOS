CREATE TABLE stations (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL,
 timezone TEXT NOT NULL, locale TEXT NOT NULL
);
CREATE TABLE sources (
 id TEXT PRIMARY KEY, station_id TEXT NOT NULL REFERENCES stations(id),
 name TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind='upnp'), endpoint TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0)
);
-- Only this table and its indexes are disposable. Station and source settings are authoritative.
CREATE TABLE catalogue_items (
 id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
 source_item_id TEXT NOT NULL, title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL,
 metadata TEXT NOT NULL CHECK(json_valid(metadata)), UNIQUE(source_id,source_item_id)
);
CREATE INDEX catalogue_source ON catalogue_items(source_id);
CREATE INDEX sources_station ON sources(station_id);
