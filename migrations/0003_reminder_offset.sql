-- Reminders are now expressed as an offset before the task's due datetime.
-- The offset (minutes) is the source of truth; reminder_at is the derived
-- column the polling query reads. Keeping reminder_at in the schema lets the
-- /api/reminders/due query stay a single index hit.
ALTER TABLE tasks ADD COLUMN reminder_offset_minutes INTEGER;
