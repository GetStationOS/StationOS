-- Source and catalogue identities survive the change from station ownership to shared use.
CREATE TABLE shared_sources (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind='upnp'),
 endpoint TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0)
);
INSERT INTO shared_sources(id,name,kind,endpoint,revision) SELECT id,name,kind,endpoint,revision FROM sources;
CREATE TABLE shared_catalogue (
 id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES shared_sources(id) ON DELETE CASCADE,
 source_item_id TEXT NOT NULL, title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT NOT NULL,
 metadata TEXT NOT NULL CHECK(json_valid(metadata)), UNIQUE(source_id,source_item_id)
);
INSERT INTO shared_catalogue SELECT * FROM catalogue_items;
DROP TABLE catalogue_items;
DROP TABLE sources;
ALTER TABLE shared_sources RENAME TO sources;
ALTER TABLE shared_catalogue RENAME TO catalogue_items;
CREATE INDEX catalogue_source ON catalogue_items(source_id);
CREATE INDEX catalogue_genre ON catalogue_items(json_extract(metadata,'$.genre'));
CREATE INDEX catalogue_album ON catalogue_items(album,source_id);
