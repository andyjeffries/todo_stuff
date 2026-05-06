-- Tracks when a reminder was delivered to the client so polling doesn't
-- re-fire the same notification. NULL = not yet delivered. Set to the UTC
-- timestamp at which /api/reminders/due first returned this row. Cleared
-- whenever reminder_at changes so a rescheduled reminder fires fresh.
ALTER TABLE tasks ADD COLUMN reminder_sent_at DATETIME;

-- The polling query reads `reminder_at <= now AND reminder_sent_at IS NULL
-- AND completed_at IS NULL`. The existing idx_tasks_reminder_at already
-- helps, but a partial index keyed on the undelivered subset is cheap and
-- keeps the per-poll scan tiny even as logbook history grows.
CREATE INDEX idx_tasks_reminder_pending
    ON tasks(reminder_at)
    WHERE reminder_sent_at IS NULL AND completed_at IS NULL;
