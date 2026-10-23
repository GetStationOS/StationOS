-- Preserve existing endpoint choices on each selected text model.
UPDATE producer_settings SET provider_routes=json_set(provider_routes,
 '$.Assistant.Thinking', json(CASE WHEN EXISTS(SELECT 1 FROM ai_connections WHERE id=json_extract(provider_routes,'$.Assistant.Connection') AND provider_type='ollama' AND thinking=1) THEN 'true' ELSE 'false' END),
 '$.Curation.Thinking', json(CASE WHEN EXISTS(SELECT 1 FROM ai_connections WHERE id=json_extract(provider_routes,'$.Curation.Connection') AND provider_type='ollama' AND thinking=1) THEN 'true' ELSE 'false' END),
 '$.Writing.Thinking', json(CASE WHEN EXISTS(SELECT 1 FROM ai_connections WHERE id=json_extract(provider_routes,'$.Writing.Connection') AND provider_type='ollama' AND thinking=1) THEN 'true' ELSE 'false' END),
 '$.Audience.Thinking', json(CASE WHEN EXISTS(SELECT 1 FROM ai_connections WHERE id=json_extract(provider_routes,'$.Audience.Connection') AND provider_type='ollama' AND thinking=1) THEN 'true' ELSE 'false' END)),
 ai_revision=ai_revision+1 WHERE id=1;
