ALTER TABLE djs ADD COLUMN conversation_speed REAL NOT NULL DEFAULT 1.0 CHECK(conversation_speed BETWEEN 0.25 AND 4.0);
