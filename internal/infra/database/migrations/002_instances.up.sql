-- Migration 002: WhatsApp instances
-- ============================================================

CREATE TABLE instances (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    phone_number    VARCHAR(50),
    status          VARCHAR(50)  NOT NULL DEFAULT 'disconnected'
                                 CHECK (status IN (
                                     'disconnected',
                                     'connecting',
                                     'qr_pending',
                                     'connected',
                                     'logged_out',
                                     'banned'
                                 )),
    jid             TEXT,                 -- WhatsApp JID once paired, e.g. 5511999990001@s.whatsapp.net
    platform        VARCHAR(100),         -- e.g. iPhone, Android, Chrome
    business_name   VARCHAR(255),
    proxy_url       TEXT,                 -- optional SOCKS5/HTTP proxy per instance
    last_connected_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_instances_workspace ON instances(workspace_id);
CREATE INDEX idx_instances_status    ON instances(status);
