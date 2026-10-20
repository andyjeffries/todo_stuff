package models

import (
	"database/sql"
	"time"
)

// Task mirrors the tasks row. Optional columns use sql.NullX so the
// "set/unset" distinction survives round-tripping through the DB.
//
// ProjectName / ProjectIcon and the Recurrence* fields are computed
// read-only fields populated by LEFT JOINs to the projects and
// recurrence_rules tables when a task is fetched for display. They are not
// persisted on the tasks row directly.
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

	RecurrenceFrequency sql.NullString
	RecurrenceInterval  sql.NullInt64
	RecurrenceType      sql.NullString

	// CalDAV identity, set when a CalDAV client created or wrote the task.
	// NULL means UID = ID and resource name = ID + ".ics". ICalExtra is the
	// JSON array of VTODO properties the server doesn't model.
	ICalUID   sql.NullString
	ICalHref  sql.NullString
	ICalExtra sql.NullString
}

func (t *Task) IsCompleted() bool { return t.CompletedAt.Valid }

// CalDAVUID is the iCalendar UID a CalDAV client knows this task by.
func (t *Task) CalDAVUID() string {
	if t.ICalUID.Valid {
		return t.ICalUID.String
	}
	return t.ID
}

// CalDAVName is the task's resource name inside its CalDAV collection.
func (t *Task) CalDAVName() string {
	if t.ICalHref.Valid {
		return t.ICalHref.String
	}
	return t.ID + ".ics"
}
