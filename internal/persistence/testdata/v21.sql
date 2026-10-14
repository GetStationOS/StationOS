ALTER TABLE producer_settings ADD COLUMN curation_model TEXT NOT NULL DEFAULT 'gpt-6.1-sol';
ALTER TABLE producer_settings ADD COLUMN speech_model TEXT NOT NULL DEFAULT 'gpt-4o-mini-tts';
ALTER TABLE producer_settings ADD COLUMN audience_model TEXT NOT NULL DEFAULT 'gpt-6-luna';
UPDATE djs SET api_key_cipher=X'' WHERE (SELECT length(api_key_cipher)>0 FROM producer_settings WHERE id=1);
