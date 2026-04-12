-- Migration 003: Messages and media
-- ============================================================

CREATE TABLE messages (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id          UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    whatsapp_message_id  VARCHAR(255),                -- WA message ID (e.g. 3EB0...)
    direction            VARCHAR(10)  NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    status               VARCHAR(50)  NOT NULL DEFAULT 'pending'
                                      CHECK (status IN (
                                          'pending',
                                          'queued',
                                          'sent',
                                          'delivered',
                                          'read',
                                          'failed',
                                          'revoked'
                                      )),
    from_jid             TEXT        NOT NULL,
    to_jid               TEXT        NOT NULL,
    chat_jid             TEXT        NOT NULL,         -- group or DM JID
    is_group             BOOLEAN     NOT NULL DEFAULT FALSE,
    type                 VARCHAR(50)  NOT NULL,        -- text | image | video | audio | document | sticker | location | contact | poll | reaction
    content              JSONB       NOT NULL DEFAULT '{}',  -- full message payload
    error_message        TEXT,
    sent_at              TIMESTAMPTZ,
    delivered_at         TIMESTAMPTZ,
    read_at              TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_messages_instance    ON messages(instance_id);
CREATE INDEX idx_messages_chat        ON messages(instance_id, chat_jid);
CREATE INDEX idx_messages_wa_id       ON messages(whatsapp_message_id) WHERE whatsapp_message_id IS NOT NULL;
CREATE INDEX idx_messages_created_at  ON messages(created_at DESC);
CREATE INDEX idx_messages_status      ON messages(status);

-- ── Media ─────────────────────────────────────────────────
CREATE TABLE media (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id   UUID        REFERENCES messages(id) ON DELETE CASCADE,
    instance_id  UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    mime_type    VARCHAR(255),
    file_size    BIGINT,
    file_name    VARCHAR(255),
    storage_path TEXT,         -- relative path inside MEDIA_STORAGE_PATH
    sha256       VARCHAR(64),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_media_message   ON media(message_id);
CREATE INDEX idx_media_instance  ON media(instance_id);
