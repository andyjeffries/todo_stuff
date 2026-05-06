package models

import (
	"database/sql"
	"time"
)

// RecurrenceRule mirrors the recurrence_rules row. A rule is shared between
// the currently-active task instance and any future instances generated when
// the user completes the active one.
//
// Frequency is one of "days", "weeks", "months", "years" (CHECK constraint
// at the DB level). Interval is the multiplier — 2 + "weeks" = every two
// weeks. RegenerationType is "on_create" (next due = previous due + interval)
// or "on_complete" (next due = completion timestamp + interval).
type RecurrenceRule struct {
	ID                     string
	UserID                 string
	Frequency              string
	Interval               int
	RegenerationType       string
	TemplateTitle          string
	TemplateNotes          sql.NullString
	TemplateProjectID      sql.NullString
	TemplateIsImportant    bool
	TemplateDueTime        sql.NullString
	TemplateReminderOffset sql.NullInt64
	CreatedAt              time.Time
}
