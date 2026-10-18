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
