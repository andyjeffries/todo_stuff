-- Pushover delivery is tracked separately from browser delivery so the two
-- channels don't race each other. Each channel atomically claims a row by
-- writing its own *_sent_at column; both can fire for the same reminder.
-- NULL = not yet delivered through Pushover. Cleared whenever reminder_at
-- changes (same trigger condition that already clears reminder_sent_at).
ALTER TABLE tasks ADD COLUMN reminder_pushover_sent_at DATETIME;

-- Partial index for the per-tick dispatcher scan: only undelivered, not yet
-- completed reminders. Mirrors idx_tasks_reminder_pending for the browser
-- channel.
CREATE INDEX idx_tasks_reminder_pushover_pending
    ON tasks(reminder_at)
    WHERE reminder_pushover_sent_at IS NULL AND completed_at IS NULL;
