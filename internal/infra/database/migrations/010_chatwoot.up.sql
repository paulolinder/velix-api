-- Maps WhatsApp JIDs to Chatwoot contact IDs per instance.
CREATE TABLE chatwoot_contacts (
    instance_id  UUID   NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    jid          TEXT   NOT NULL,
    contact_id   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, jid)
);

-- Maps WhatsApp chat JIDs to Chatwoot conversation IDs per instance.
CREATE TABLE chatwoot_conversations (
    instance_id     UUID   NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    chat_jid        TEXT   NOT NULL,
    conversation_id BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, chat_jid)
);

-- Allows reverse lookup: conversation_id → chat_jid (used when Chatwoot webhook arrives).
CREATE INDEX idx_chatwoot_conversations_conv_id
    ON chatwoot_conversations (instance_id, conversation_id);
