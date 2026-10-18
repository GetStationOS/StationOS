-- Richer dynamic rules preserve existing authoritative choices.
CREATE TABLE playlist_rules_new (
 id TEXT PRIMARY KEY, playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('genre','album','artist','composer','year','decade','filter')),
 value TEXT NOT NULL, source_id TEXT NOT NULL,
 UNIQUE(playlist_id,kind,value,source_id)
);
INSERT INTO playlist_rules_new SELECT * FROM playlist_rules;
DROP TABLE playlist_rules;
ALTER TABLE playlist_rules_new RENAME TO playlist_rules;
CREATE TABLE catalogue_credits (
 content_id TEXT NOT NULL REFERENCES catalogue_items(id) ON DELETE CASCADE,
 role TEXT NOT NULL CHECK(role IN ('artist','composer')), name TEXT NOT NULL,
 PRIMARY KEY(content_id,role,name)
);
CREATE INDEX catalogue_credits_name ON catalogue_credits(role,name,content_id);
INSERT INTO catalogue_credits WITH RECURSIVE split(content_id,role,name,rest) AS (
 SELECT id,'artist','',coalesce(json_extract(metadata,'$.artist'),'')||';' FROM catalogue_items 
 UNION ALL SELECT id,'composer','',coalesce(json_extract(metadata,'$.composer'),'')||';' FROM catalogue_items 
 UNION ALL SELECT content_id,role,trim(substr(rest,1,instr(rest,';')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,';')+1) FROM split WHERE rest<>''
) SELECT DISTINCT content_id,role,name FROM split WHERE name<>'' ;
CREATE TRIGGER catalogue_credits_insert AFTER INSERT ON catalogue_items BEGIN
INSERT INTO catalogue_credits SELECT * FROM (WITH RECURSIVE split(content_id,role,name,rest) AS (
 SELECT id,'artist','',coalesce(json_extract(metadata,'$.artist'),'')||';' FROM catalogue_items WHERE id=NEW.id
 UNION ALL SELECT id,'composer','',coalesce(json_extract(metadata,'$.composer'),'')||';' FROM catalogue_items WHERE id=NEW.id
 UNION ALL SELECT content_id,role,trim(substr(rest,1,instr(rest,';')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,';')+1) FROM split WHERE rest<>''
) SELECT DISTINCT content_id,role,name FROM split WHERE name<>'' );
END;
CREATE TRIGGER catalogue_credits_update AFTER UPDATE OF metadata ON catalogue_items BEGIN
DELETE FROM catalogue_credits WHERE content_id=OLD.id;
INSERT INTO catalogue_credits SELECT * FROM (WITH RECURSIVE split(content_id,role,name,rest) AS (
 SELECT id,'artist','',coalesce(json_extract(metadata,'$.artist'),'')||';' FROM catalogue_items WHERE id=NEW.id
 UNION ALL SELECT id,'composer','',coalesce(json_extract(metadata,'$.composer'),'')||';' FROM catalogue_items WHERE id=NEW.id
 UNION ALL SELECT content_id,role,trim(substr(rest,1,instr(rest,';')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,';')+1) FROM split WHERE rest<>''
) SELECT DISTINCT content_id,role,name FROM split WHERE name<>'' );
END;
