-- Buffers WhatsApp history sync messages for replay into Chatwoot.
-- Rows are deleted after successful sync to avoid duplicate replay.
CREATE TABLE IF NOT EXISTS chatwoot_history_buffer (
    instance_id UUID    NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    message_id  TEXT    NOT NULL,
    chat_jid    TEXT    NOT NULL,
    sender_jid  TEXT    NOT NULL,
    from_me     BOOLEAN NOT NULL DEFAULT FALSE,
    text        TEXT    NOT NULL DEFAULT '',
    msg_type    TEXT    NOT NULL DEFAULT 'text',
    push_name   TEXT    NOT NULL DEFAULT '',
    timestamp   TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_chatwoot_history_buffer_chat
    ON chatwoot_history_buffer (instance_id, chat_jid, timestamp);
