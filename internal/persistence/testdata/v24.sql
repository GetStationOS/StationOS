-- DJ identity and editorial configuration remain unchanged. Model selection is
-- owned exclusively by global AI settings; retain that selected model as-is.
ALTER TABLE djs DROP COLUMN model;
UPDATE producer_settings SET ai_revision=ai_revision+1 WHERE id=1;
