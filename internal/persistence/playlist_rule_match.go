package persistence

// One predicate keeps playout, counts, review and refresh eligibility consistent.
const playlistRuleMatch = `( (r.kind='genre' AND EXISTS(SELECT 1 FROM catalogue_genres g WHERE g.content_id=i.id AND g.genre=r.value))
 OR (r.kind='album' AND r.value=i.album AND r.source_id=i.source_id)
 OR (r.kind IN ('artist','composer') AND EXISTS(SELECT 1 FROM catalogue_credits c WHERE c.content_id=i.id AND c.role=r.kind AND c.name=r.value))
 OR (r.kind='year' AND json_extract(i.metadata,'$.year')=CAST(r.value AS INTEGER))
 OR (r.kind='decade' AND json_extract(i.metadata,'$.year') BETWEEN CAST(r.value AS INTEGER) AND CAST(r.value AS INTEGER)+9)
 OR (r.kind='filter' AND CASE WHEN json_valid(r.value) THEN
 (coalesce(json_extract(r.value,'$.genre'),'')='' OR EXISTS(SELECT 1 FROM catalogue_genres g WHERE g.content_id=i.id AND g.genre=json_extract(r.value,'$.genre')))
 AND (coalesce(json_extract(r.value,'$.artist'),'')='' OR EXISTS(SELECT 1 FROM catalogue_credits c WHERE c.content_id=i.id AND c.role='artist' AND c.name=json_extract(r.value,'$.artist')))
 AND (coalesce(json_extract(r.value,'$.composer'),'')='' OR EXISTS(SELECT 1 FROM catalogue_credits c WHERE c.content_id=i.id AND c.role='composer' AND c.name=json_extract(r.value,'$.composer')))
 AND (coalesce(json_extract(r.value,'$.album'),'')='' OR i.album=json_extract(r.value,'$.album'))
 AND (coalesce(json_extract(r.value,'$.source_id'),'')='' OR i.source_id=json_extract(r.value,'$.source_id'))
 AND (coalesce(json_extract(r.value,'$.year_from'),0)=0 OR coalesce(json_extract(i.metadata,'$.year'),0)>=json_extract(r.value,'$.year_from'))
 AND (coalesce(json_extract(r.value,'$.year_to'),0)=0 OR (coalesce(json_extract(i.metadata,'$.year'),0)>0 AND json_extract(i.metadata,'$.year')<=json_extract(r.value,'$.year_to')))
 ELSE 0 END))`

// Values within a category are alternatives. Every selected category must match.
// Years and decades share one date category. No rules means no automatic tracks.
const playlistRuleCategory = `CASE WHEN r.kind IN ('year','decade') THEN 'date' ELSE r.kind END`
const playlistRulesMatch = `SELECT 1 FROM playlist_rules r WHERE r.playlist_id=?
 GROUP BY r.playlist_id
 HAVING count(DISTINCT ` + playlistRuleCategory + `) =
 count(DISTINCT CASE WHEN ` + playlistRuleMatch + ` THEN ` + playlistRuleCategory + ` END)`

// A station's explicit source choices constrain every playlist candidate path.
const playlistSources = `WITH selected_sources AS (
 SELECT ss.source_id FROM station_sources ss JOIN playlists pl ON pl.station_id=ss.station_id WHERE pl.id=?
) `
const playlistSourceMatch = `i.source_id IN (SELECT source_id FROM selected_sources)`
