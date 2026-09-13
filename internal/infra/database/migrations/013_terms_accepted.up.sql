-- Migration 013: add terms_accepted_at to workspaces
-- Registrations created before this migration have NULL (pre-existing accounts).
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS terms_accepted_at TIMESTAMPTZ;
