CREATE TABLE playlist_refresh_items (
 playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
 content_id TEXT NOT NULL, source_id TEXT NOT NULL, album TEXT NOT NULL,
 PRIMARY KEY(playlist_id,content_id)
);
-- Existing eligible items establish the initial baseline; first refresh must not
-- claim the entire existing playlist was newly added.
INSERT INTO playlist_refresh_items(playlist_id,content_id,source_id,album)
 SELECT p.id,i.id,i.source_id,i.album FROM playlists p JOIN catalogue_items i
 ON (EXISTS(SELECT 1 FROM playlist_selections s WHERE s.playlist_id=p.id AND s.content_id=i.id AND s.included=1)
 OR EXISTS(SELECT 1 FROM playlist_rules r WHERE r.playlist_id=p.id AND ((r.kind='genre' AND r.value=json_extract(i.metadata,'$.genre')) OR (r.kind='album' AND r.value=i.album AND r.source_id=i.source_id))))
 WHERE NOT EXISTS(SELECT 1 FROM playlist_selections x WHERE x.playlist_id=p.id AND x.content_id=i.id AND x.included=0);
INSERT OR IGNORE INTO playlist_refresh_items(playlist_id,content_id,source_id,album)
 SELECT playlist_id,content_id,coalesce(json_extract(snapshot,'$.source_id'),''),coalesce(json_extract(snapshot,'$.album'),'')
 FROM playlist_selections WHERE included=1;
