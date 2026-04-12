-- Migration 001: Core tables — workspaces, users, api_keys
-- ============================================================

-- UUID extension (required for gen_random_uuid)
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ── Workspaces (tenants) ──────────────────────────────────
CREATE TABLE workspaces (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(255) NOT NULL,
    slug       VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT workspaces_slug_unique UNIQUE (slug)
);

-- ── Users ─────────────────────────────────────────────────
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email         VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role          VARCHAR(50)  NOT NULL DEFAULT 'developer'
                               CHECK (role IN ('admin', 'developer', 'viewer')),
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT users_email_unique UNIQUE (email)
);

CREATE INDEX idx_users_workspace ON users(workspace_id);

-- ── API Keys ──────────────────────────────────────────────
-- key_hash stores a bcrypt hash of the actual key.
-- key_prefix stores the first ~12 chars for display (e.g. wapi_live_xxxx).
CREATE TABLE api_keys (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      UUID        REFERENCES users(id) ON DELETE SET NULL,
    key_hash     VARCHAR(255) NOT NULL,
    key_prefix   VARCHAR(20)  NOT NULL,  -- displayed to user, not secret
    name         VARCHAR(255),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_keys_workspace ON api_keys(workspace_id);
CREATE INDEX idx_api_keys_prefix    ON api_keys(key_prefix);
