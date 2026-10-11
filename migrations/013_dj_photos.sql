CREATE TABLE dj_photos (
    dj_id TEXT PRIMARY KEY REFERENCES djs(id) ON DELETE CASCADE,
    data BLOB NOT NULL CHECK(length(data) > 0 AND length(data) <= 1048576)
);
