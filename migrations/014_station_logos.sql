CREATE TABLE station_logos (
 station_id TEXT PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
 data BLOB NOT NULL CHECK(length(data)>0 AND length(data)<=5242880),
 version TEXT NOT NULL
);
