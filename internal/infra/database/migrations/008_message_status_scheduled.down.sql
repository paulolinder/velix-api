ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_status_check;

ALTER TABLE messages
    ADD CONSTRAINT messages_status_check
    CHECK (status IN (
        'pending',
        'queued',
        'sent',
        'delivered',
        'read',
        'failed',
        'revoked'
    ));
