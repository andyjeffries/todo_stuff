package models

import (
	"database/sql"
	"time"
)

// Task mirrors the tasks row. Optional columns use sql.NullX so the
// "set/unset" distinction survives round-tripping through the DB.
//
// ProjectName / ProjectIcon are computed read-only fields populated by a
// LEFT JOIN to the projects table when a task is fetched for display.
// They are not persisted on the tasks row directly.
type Task struct {
	ID               string
	UserID           string
	ProjectID        sql.NullString
	Title            string
	Notes            sql.NullString
	NotesHTML        sql.NullString
	IsImportant      bool
	DueDate          sql.NullTime   // DATE column; only the y-m-d portion is meaningful
	DueTime          sql.NullString // TIME column stored as "HH:MM" or "HH:MM:SS"
	ReminderAt            sql.NullTime
	ReminderOffsetMinutes sql.NullInt64
	ReminderSentAt        sql.NullTime
	CompletedAt           sql.NullTime
	Position         int64
	RecurrenceRuleID sql.NullString
	CreatedAt        time.Time
	UpdatedAt        time.Time

	ProjectName sql.NullString
	ProjectIcon sql.NullString
}

func (t *Task) IsCompleted() bool { return t.CompletedAt.Valid }
