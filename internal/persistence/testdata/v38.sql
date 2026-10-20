ALTER TABLE djs ADD COLUMN speech_voices TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(speech_voices) AND json_type(speech_voices)='object');
