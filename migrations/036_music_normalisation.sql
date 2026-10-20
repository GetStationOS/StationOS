ALTER TABLE station_mixer ADD COLUMN normalise_music INTEGER NOT NULL DEFAULT 0 CHECK(normalise_music IN (0,1));
