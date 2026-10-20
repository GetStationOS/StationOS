ALTER TABLE djs ADD COLUMN speech_connection TEXT NOT NULL DEFAULT 'openai';
ALTER TABLE djs ADD COLUMN speech_model TEXT NOT NULL DEFAULT 'gpt-4o-mini-tts';
ALTER TABLE djs ADD COLUMN speech_settings TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(speech_settings) AND json_type(speech_settings)='object');
UPDATE djs SET
 speech_connection=COALESCE(NULLIF(json_extract((SELECT provider_routes FROM producer_settings WHERE id=1),'$.Speech.Connection'),''),'openai'),
 speech_model=CASE WHEN COALESCE((SELECT provider_type FROM ai_connections WHERE id=COALESCE(NULLIF(json_extract((SELECT provider_routes FROM producer_settings WHERE id=1),'$.Speech.Connection'),''),'openai')),'openai')='chatterbox' THEN 'chatterbox-nano' ELSE (SELECT speech_model FROM producer_settings WHERE id=1) END;
-- Preserve OpenAI preferences even if the installation currently selects Nano.
UPDATE djs SET speech_settings=json_patch(
 json_object('openai',json_object('model',CASE WHEN speech_model='chatterbox-nano' THEN 'gpt-4o-mini-tts' ELSE speech_model END,'voice',voice,'speed',conversation_speed,'vibe',vibe)),
 json_object(speech_connection,json_object('model',speech_model,
 'voice',CASE WHEN COALESCE((SELECT provider_type FROM ai_connections WHERE id=speech_connection),'openai')='chatterbox' THEN COALESCE((SELECT value FROM json_each(djs.speech_voices) WHERE key=djs.speech_connection),'') ELSE voice END,
 'speed',CASE WHEN COALESCE((SELECT provider_type FROM ai_connections WHERE id=speech_connection),'openai')='chatterbox' THEN 1 ELSE conversation_speed END,
 'vibe',CASE WHEN COALESCE((SELECT provider_type FROM ai_connections WHERE id=speech_connection),'openai')='chatterbox' THEN '' ELSE vibe END)));
