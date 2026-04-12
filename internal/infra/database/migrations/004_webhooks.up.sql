-- Migration 004: Webhooks and delivery log
-- ============================================================

CREATE TABLE webhooks (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    instance_id  UUID        REFERENCES instances(id) ON DELETE CASCADE,  -- NULL = all instances
    url          TEXT        NOT NULL,
    secret_hash  VARCHAR(255),   -- HMAC-SHA256 signing key (stored hashed)
    events       TEXT[]      NOT NULL DEFAULT '{}',  -- e.g. {'messages.received','status.update'}
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhooks_workspace ON webhooks(workspace_id);
CREATE INDEX idx_webhooks_instance  ON webhooks(instance_id);
CREATE INDEX idx_webhooks_enabled   ON webhooks(enabled) WHERE enabled = TRUE;

-- ── Webhook Deliveries ────────────────────────────────────
CREATE TABLE webhook_deliveries (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id    UUID        NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type    VARCHAR(100) NOT NULL,
    payload       JSONB       NOT NULL,
    status_code   INT,
    response_body TEXT,
    attempt_count INT         NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    delivered_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wd_webhook      ON webhook_deliveries(webhook_id);
CREATE INDEX idx_wd_retry        ON webhook_deliveries(next_retry_at) WHERE delivered_at IS NULL;
CREATE INDEX idx_wd_created_at   ON webhook_deliveries(created_at DESC);
