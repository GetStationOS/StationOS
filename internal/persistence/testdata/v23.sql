CREATE TABLE ai_connections (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 provider_type TEXT NOT NULL CHECK(provider_type IN ('openai','ollama','compatible','anthropic','gemini')),
 address TEXT NOT NULL,
 api_key_cipher BLOB NOT NULL DEFAULT X'',
 context_tokens INTEGER NOT NULL DEFAULT 8192,
 output_tokens INTEGER NOT NULL DEFAULT 2048
);
ALTER TABLE producer_settings ADD COLUMN provider_routes TEXT NOT NULL DEFAULT '{}';
ALTER TABLE producer_settings ADD COLUMN ai_revision INTEGER NOT NULL DEFAULT 0;
