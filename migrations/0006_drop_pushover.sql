-- Pushover is gone. Phone reminders now arrive natively through the CalDAV
-- endpoint (Apple Reminders alarms), so a Pushover push on top would alert
-- twice. Drop its delivery marker and the per-user key/toggle.
--
-- The partial index references reminder_pushover_sent_at, and SQLite refuses
-- DROP COLUMN on an indexed column, so the index has to go first.
DROP INDEX IF EXISTS idx_tasks_reminder_pushover_pending;

ALTER TABLE tasks DROP COLUMN reminder_pushover_sent_at;
ALTER TABLE users DROP COLUMN pushover_user_key;
ALTER TABLE users DROP COLUMN pushover_enabled;
