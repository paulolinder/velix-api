-- Migration 005: Audit log
-- Every user action and API Key action is recorded here.
-- ============================================================

CREATE TABLE audit_logs (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        REFERENCES workspaces(id) ON DELETE SET NULL,
    actor_type    VARCHAR(50) NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system')),
    actor_id      UUID,
    actor_email   VARCHAR(255),
    action        VARCHAR(255) NOT NULL,   -- e.g. instance.created, message.sent, webhook.deleted
    resource_type VARCHAR(100),            -- e.g. instance, message, webhook
    resource_id   UUID,
    metadata      JSONB,                   -- extra context (old/new values, etc.)
    ip_address    INET,
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_workspace   ON audit_logs(workspace_id);
CREATE INDEX idx_audit_actor       ON audit_logs(actor_id);
CREATE INDEX idx_audit_action      ON audit_logs(action);
CREATE INDEX idx_audit_created_at  ON audit_logs(created_at DESC);
