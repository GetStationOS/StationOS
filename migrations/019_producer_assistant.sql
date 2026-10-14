CREATE TABLE producer_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), model TEXT NOT NULL,
 api_key_cipher BLOB NOT NULL DEFAULT X''
);
INSERT INTO producer_settings(id,model) VALUES(1,'gpt-6.1-sol');
CREATE TABLE producer_conversations (
 id TEXT PRIMARY KEY, station_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'idle', turn_id TEXT NOT NULL DEFAULT '',
 transcript TEXT NOT NULL DEFAULT '[]', created TEXT NOT NULL
);
CREATE TABLE producer_turns (
 conversation_id TEXT NOT NULL REFERENCES producer_conversations(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL, PRIMARY KEY(conversation_id,request_id)
);
CREATE TABLE producer_messages (
 id INTEGER PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES producer_conversations(id) ON DELETE CASCADE,
 role TEXT NOT NULL, text TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', created TEXT NOT NULL
);
CREATE INDEX producer_message_history ON producer_messages(conversation_id,id);
