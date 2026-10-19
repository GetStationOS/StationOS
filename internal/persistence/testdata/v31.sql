-- Preserve existing stations' access to their currently configured sources.
CREATE TABLE station_sources (
 station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
 source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
 PRIMARY KEY(station_id,source_id)
);
CREATE INDEX station_sources_source ON station_sources(source_id,station_id);
INSERT INTO station_sources(station_id,source_id) SELECT stations.id,sources.id FROM stations CROSS JOIN sources;
