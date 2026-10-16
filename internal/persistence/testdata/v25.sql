ALTER TABLE ai_connections ADD COLUMN request_timeout_seconds INTEGER NOT NULL DEFAULT 180 CHECK(request_timeout_seconds BETWEEN 15 AND 600);
UPDATE ai_connections SET request_timeout_seconds=60 WHERE provider_type='openai';
UPDATE producer_settings SET ai_revision=ai_revision+1 WHERE id=1;
