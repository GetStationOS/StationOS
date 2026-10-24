ALTER TABLE station_shows ADD COLUMN weekdays TEXT NOT NULL DEFAULT '[]';
ALTER TABLE station_shows ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
UPDATE station_shows SET weekdays=json_array(weekday);
ALTER TABLE station_shows ADD COLUMN slot_ids TEXT NOT NULL DEFAULT '{}';
