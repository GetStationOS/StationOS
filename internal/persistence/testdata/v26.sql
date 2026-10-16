CREATE TABLE transmission_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 station_id TEXT NOT NULL,
 started INTEGER NOT NULL,
 ended INTEGER NOT NULL DEFAULT 0,
 kind TEXT NOT NULL,
 outcome TEXT NOT NULL,
 snapshot TEXT NOT NULL
);
CREATE INDEX transmission_station_time ON transmission_events(station_id,started DESC,id DESC);
CREATE TABLE transmission_incidents (
 id TEXT PRIMARY KEY,
 station_id TEXT NOT NULL,
 code TEXT NOT NULL,
 first_at INTEGER NOT NULL,
 latest_at INTEGER NOT NULL,
 recovered_at INTEGER NOT NULL DEFAULT 0,
 acknowledged_at INTEGER NOT NULL DEFAULT 0,
 occurrences INTEGER NOT NULL,
 snapshot TEXT NOT NULL
);
CREATE UNIQUE INDEX transmission_active_incident ON transmission_incidents(station_id,code) WHERE recovered_at=0;
