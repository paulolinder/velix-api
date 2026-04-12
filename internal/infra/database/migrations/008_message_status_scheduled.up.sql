-- Migration 008: Add 'scheduled' and 'cancelled' to messages.status CHECK constraint
-- PostgreSQL requires dropping and recreating the constraint.

ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_status_check;

ALTER TABLE messages
    ADD CONSTRAINT messages_status_check
    CHECK (status IN (
        'pending',
        'queued',
        'scheduled',
        'sent',
        'delivered',
        'read',
        'failed',
        'revoked',
        'cancelled'
    ));
