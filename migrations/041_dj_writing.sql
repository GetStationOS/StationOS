ALTER TABLE producer_settings ADD COLUMN writing_model TEXT NOT NULL DEFAULT 'gpt-6-luna';
UPDATE producer_settings SET writing_model=curation_model,
 provider_routes=json_set(provider_routes,'$.Writing',json(COALESCE(json_extract(provider_routes,'$.Curation'),'{"Connection":""}'))),
 ai_revision=ai_revision+1;
-- OpenAI planning now uses Decisions; preserve the former model for DJ writing.
UPDATE producer_settings SET curation_model='gpt-6-luna'
WHERE COALESCE(json_extract(provider_routes,'$.Curation.Connection'),'') IN ('','openai')
 OR EXISTS(SELECT 1 FROM ai_connections WHERE id=json_extract(provider_routes,'$.Curation.Connection') AND provider_type='openai');
