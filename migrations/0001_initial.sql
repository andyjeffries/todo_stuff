CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    pushover_user_key TEXT,
    pushover_enabled INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    icon TEXT,
    color TEXT,
    position INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_projects_user_id ON projects(user_id);
CREATE INDEX idx_projects_position ON projects(user_id, position);

CREATE TABLE recurrence_rules (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    frequency TEXT NOT NULL CHECK (frequency IN ('days','weeks','months','years')),
    interval INTEGER NOT NULL DEFAULT 1,
    regeneration_type TEXT NOT NULL CHECK (regeneration_type IN ('on_create','on_complete')),
    template_title TEXT NOT NULL,
    template_notes TEXT,
    template_project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    template_is_important INTEGER NOT NULL DEFAULT 0,
    template_due_time TIME,
    template_reminder_offset INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_recurrence_rules_user_id ON recurrence_rules(user_id);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    notes TEXT,
    notes_html TEXT,
    is_important INTEGER NOT NULL DEFAULT 0,
    due_date DATE,
    due_time TIME,
    reminder_at DATETIME,
    completed_at DATETIME,
    position INTEGER NOT NULL,
    recurrence_rule_id TEXT REFERENCES recurrence_rules(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_tasks_user_id ON tasks(user_id);
CREATE INDEX idx_tasks_project_id ON tasks(project_id);
CREATE INDEX idx_tasks_due_date ON tasks(due_date);
CREATE INDEX idx_tasks_reminder_at ON tasks(reminder_at);
CREATE INDEX idx_tasks_completed_at ON tasks(completed_at);
