-- Rebuildable genre membership; raw source metadata remains authoritative.
CREATE TABLE catalogue_genres (
 content_id TEXT NOT NULL REFERENCES catalogue_items(id) ON DELETE CASCADE,
 genre TEXT NOT NULL,
 PRIMARY KEY(content_id,genre)
);
CREATE INDEX catalogue_genres_name ON catalogue_genres(genre,content_id);
INSERT INTO catalogue_genres(content_id,genre)
WITH RECURSIVE split(content_id,genre,rest) AS (
 SELECT id,'',coalesce(json_extract(metadata,'$.genre'),'')||',' FROM catalogue_items
 UNION ALL
 SELECT content_id,trim(substr(rest,1,instr(rest,',')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,',')+1)
 FROM split WHERE rest<>''
)
SELECT DISTINCT content_id,genre FROM split WHERE genre<>'';
CREATE TRIGGER catalogue_genres_insert AFTER INSERT ON catalogue_items BEGIN
 INSERT INTO catalogue_genres(content_id,genre)
 SELECT content_id,genre FROM (
WITH RECURSIVE split(content_id,genre,rest) AS (
 SELECT id,'',coalesce(json_extract(metadata,'$.genre'),'')||',' FROM catalogue_items WHERE id=NEW.id
 UNION ALL
 SELECT content_id,trim(substr(rest,1,instr(rest,',')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,',')+1)
 FROM split WHERE rest<>''
)
SELECT DISTINCT content_id,genre FROM split WHERE genre<>'');
END;
CREATE TRIGGER catalogue_genres_update AFTER UPDATE OF metadata ON catalogue_items BEGIN
 DELETE FROM catalogue_genres WHERE content_id=OLD.id;
 INSERT INTO catalogue_genres(content_id,genre)
 SELECT content_id,genre FROM (
WITH RECURSIVE split(content_id,genre,rest) AS (
 SELECT id,'',coalesce(json_extract(metadata,'$.genre'),'')||',' FROM catalogue_items WHERE id=NEW.id
 UNION ALL
 SELECT content_id,trim(substr(rest,1,instr(rest,',')-1),char(9)||char(10)||char(13)||' '),substr(rest,instr(rest,',')+1)
 FROM split WHERE rest<>''
)
SELECT DISTINCT content_id,genre FROM split WHERE genre<>'');
END;
