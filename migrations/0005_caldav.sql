-- CalDAV: Apple Reminders (and any other CalDAV client) see each project as
-- a VTODO list, plus an "Inbox" list for tasks with no project.
--
-- A CalDAV client picks its own UID and resource name when it creates a
-- reminder, so both are stored. NULL means "one we made": UID = tasks.id and
-- resource name = tasks.id || '.ics'.
--
-- ical_extra holds the VTODO properties we don't model (X-APPLE-*, RELATED-TO
-- and so on) as a JSON array, re-emitted on every GET so a round trip through
-- the server doesn't strip what the client wrote.
ALTER TABLE tasks ADD COLUMN ical_uid TEXT;
ALTER TABLE tasks ADD COLUMN ical_href TEXT;
ALTER TABLE tasks ADD COLUMN ical_extra TEXT;

CREATE UNIQUE INDEX idx_tasks_ical_uid ON tasks(user_id, ical_uid) WHERE ical_uid IS NOT NULL;

-- App passwords: one per device, the password for /dav. They're long random
-- tokens, so a SHA-256 is enough for Basic auth (bcrypt would cost ~250 ms on
-- every one of the dozens of requests a sync makes).
--
-- digest_ha1 is MD5(email:realm:password) for HTTP Digest, which is what iOS
-- uses over plain HTTP: it won't send a Basic password without TLS. It's
-- tied to the email, so an email change needs a new app password.
CREATE TABLE app_passwords (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    digest_ha1 TEXT NOT NULL,
    last_used_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_app_passwords_user_id ON app_passwords(user_id);
