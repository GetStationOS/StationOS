ALTER TABLE ai_connections ADD COLUMN thinking INTEGER NOT NULL DEFAULT 0 CHECK (thinking IN (0,1));
UPDATE producer_settings SET ai_revision=ai_revision+1 WHERE id=1;
