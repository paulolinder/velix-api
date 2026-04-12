ALTER TABLE messages ADD COLUMN scheduled_at TIMESTAMPTZ;

CREATE INDEX idx_messages_scheduled
    ON messages (scheduled_at)
    WHERE status = 'scheduled' AND scheduled_at IS NOT NULL;
