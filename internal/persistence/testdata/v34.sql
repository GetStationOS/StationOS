-- Preserve the intent of existing format settings as ordinary agent instructions.
UPDATE station_shows SET music_direction=trim(music_direction || char(10) ||
 CASE rotation_format
 WHEN 'artist' THEN 'Concentrate on one performer, mixing tracks from different albums. Repeated performers are intentional.'
 WHEN 'album' THEN 'Focus on one album. Consecutive tracks from that album are intentional.'
 ELSE '' END), revision=revision+1
WHERE rotation_format IN ('artist','album');
ALTER TABLE station_shows DROP COLUMN rotation_format;
