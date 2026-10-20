CREATE TABLE ai_connections_new (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 provider_type TEXT NOT NULL CHECK(provider_type IN ('openai','ollama','compatible','anthropic','gemini','chatterbox')),
 address TEXT NOT NULL,
 api_key_cipher BLOB NOT NULL DEFAULT X'',
 context_tokens INTEGER NOT NULL DEFAULT 8192,
 output_tokens INTEGER NOT NULL DEFAULT 2048,
 request_timeout_seconds INTEGER NOT NULL DEFAULT 180 CHECK(request_timeout_seconds BETWEEN 15 AND 600)
);
INSERT INTO ai_connections_new SELECT id,name,provider_type,address,api_key_cipher,context_tokens,output_tokens,request_timeout_seconds FROM ai_connections;
DROP TABLE ai_connections;
ALTER TABLE ai_connections_new RENAME TO ai_connections;
