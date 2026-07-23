-- Delivery-key idempotency (RFC-0021 quick win): a retried send with the same
-- key replays the original row instead of inserting a duplicate. NULL keys
-- keep the legacy at-least-once behavior, so the unique index is partial.

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS delivery_key VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS uq_notifications_delivery_key
    ON notifications(delivery_key)
    WHERE delivery_key IS NOT NULL;
