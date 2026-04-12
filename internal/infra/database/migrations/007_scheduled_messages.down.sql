DROP INDEX IF EXISTS idx_messages_scheduled;
ALTER TABLE messages DROP COLUMN scheduled_at;
