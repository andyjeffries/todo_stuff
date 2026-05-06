// Package services holds the business-logic layer for projects, tasks,
// recurrence, and notifications. Services are user-scoped: every operation
// takes a userID and refuses to touch rows owned by anyone else.
package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// markdown is the shared goldmark renderer. Default options leave HTML
// escaping ON, so user-supplied notes can't smuggle <script> tags into the
// detail panel. GFM gives us tables, strikethrough, and autolinks.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// renderNotesHTML converts notes markdown to HTML. Returns Valid=false for
// empty input so the column is stored as NULL rather than an empty string.
func renderNotesHTML(src string) (sql.NullString, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return sql.NullString{}, nil
	}
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		return sql.NullString{}, fmt.Errorf("render notes markdown: %w", err)
	}
	return sql.NullString{String: buf.String(), Valid: true}, nil
}

var (
	ErrTaskNotFound = errors.New("services: task not found")
)

// View is one of the smart-list filters used by the sidebar. ListByView
// translates these into SQL filters.
type View string

const (
	ViewToday    View = "today"
	ViewInbox    View = "inbox"
	ViewUpcoming View = "upcoming"
	ViewAnytime  View = "anytime"
	ViewLogbook  View = "logbook"
)

type Tasks struct {
	db  *sql.DB
	now func() time.Time
}

func NewTasks(db *sql.DB) *Tasks {
	return &Tasks{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// ----------------------------------------------------------------- Create ---

type CreateTaskParams struct {
	Title       string
	Notes       string
	ProjectID   string // empty = inbox
	IsImportant bool
	DueDate     *time.Time // nil = unscheduled
	DueTime     string     // "HH:MM" or "" — requires DueDate when set
}

func (t *Tasks) Create(ctx context.Context, userID string, p CreateTaskParams) (*models.Task, error) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return nil, errors.New("services: title is required")
	}

	now := t.now()
	task := &models.Task{
		ID:          uuid.NewString(),
		UserID:      userID,
		Title:       title,
		IsImportant: p.IsImportant,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if p.Notes = strings.TrimSpace(p.Notes); p.Notes != "" {
		task.Notes = sql.NullString{String: p.Notes, Valid: true}
		html, err := renderNotesHTML(p.Notes)
		if err != nil {
			return nil, err
		}
		task.NotesHTML = html
	}
	if pid := strings.TrimSpace(p.ProjectID); pid != "" {
		task.ProjectID = sql.NullString{String: pid, Valid: true}
	}
	if p.DueDate != nil {
		task.DueDate = sql.NullTime{Time: *p.DueDate, Valid: true}
		if dt := strings.TrimSpace(p.DueTime); dt != "" {
			task.DueTime = sql.NullString{String: dt, Valid: true}
		}
	}

	// Position: append to the end of the user's list.
	_, err := t.db.ExecContext(ctx, `
        INSERT INTO tasks (
            id, user_id, project_id, title, notes, notes_html,
            is_important, due_date, due_time,
            position, created_at, updated_at
        ) VALUES (
            ?, ?, ?, ?, ?, ?,
            ?, ?, ?,
            COALESCE((SELECT MAX(position) FROM tasks WHERE user_id = ?), 0) + 1,
            ?, ?
        )
    `,
		task.ID, task.UserID, task.ProjectID, task.Title, task.Notes, task.NotesHTML,
		task.IsImportant, task.DueDate, task.DueTime,
		userID,
		task.CreatedAt, task.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	// Re-fetch to pick up the position the SELECT computed.
	return t.Get(ctx, userID, task.ID)
}

// ------------------------------------------------------------------- Get ---

func (t *Tasks) Get(ctx context.Context, userID, id string) (*models.Task, error) {
	row := t.db.QueryRowContext(ctx, taskSelect+` WHERE t.id = ? AND t.user_id = ?`, id, userID)
	return scanTask(row)
}

// ------------------------------------------------------------------- List ---

// ListByProject returns the project's incomplete tasks, ordered by position.
// Completed tasks are still surfaced via the Logbook view.
func (t *Tasks) ListByProject(ctx context.Context, userID, projectID string) ([]models.Task, error) {
	rows, err := t.db.QueryContext(ctx,
		taskSelect+` WHERE t.user_id = ? AND t.project_id = ? AND t.completed_at IS NULL ORDER BY t.position`,
		userID, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project tasks: %w", err)
	}
	defer rows.Close()

	var out []models.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *task)
	}
	return out, rows.Err()
}

func (t *Tasks) ListByView(ctx context.Context, userID string, view View) ([]models.Task, error) {
	where, order, args := viewQuery(userID, view, t.now())
	rows, err := t.db.QueryContext(ctx, taskSelect+` WHERE `+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []models.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *task)
	}
	return out, rows.Err()
}

// viewQuery returns the WHERE clause, ORDER BY clause, and args for a view.
// Column references must qualify with `t.` because taskSelect joins the
// projects table.
//
// Inbox semantics deliberately diverge from the master plan: an Inbox task is
// one with neither a project nor a due date — i.e. truly unprocessed work.
// Once you give a task a project or a date you've already filed it.
//
// Note on `date(t.due_date)`: go-sqlite3 stores time.Time as a full
// "YYYY-MM-DD HH:MM:SS+00:00" string, but we conceptually only care about
// the date portion. SQLite's `date()` normalises both formats so the
// comparison is correct regardless of how a row was written.
func viewQuery(userID string, view View, now time.Time) (where, order string, args []any) {
	today := now.Format("2006-01-02")
	switch view {
	case ViewToday:
		return `t.user_id = ? AND t.completed_at IS NULL AND (t.due_date IS NULL OR date(t.due_date) <= ?)`,
			`t.due_time IS NULL, t.due_time, t.position`,
			[]any{userID, today}
	case ViewInbox:
		return `t.user_id = ? AND t.completed_at IS NULL AND t.project_id IS NULL AND t.due_date IS NULL`,
			`t.position`,
			[]any{userID}
	case ViewUpcoming:
		return `t.user_id = ? AND t.completed_at IS NULL AND date(t.due_date) > ?`,
			`t.due_date, t.due_time IS NULL, t.due_time, t.position`,
			[]any{userID, today}
	case ViewLogbook:
		return `t.user_id = ? AND t.completed_at IS NOT NULL`,
			`t.completed_at DESC`,
			[]any{userID}
	default: // ViewAnytime
		return `t.user_id = ? AND t.completed_at IS NULL`,
			`t.project_id IS NULL, t.position`,
			[]any{userID}
	}
}

// ----------------------------------------------------------------- Update ---

// TaskPatch is a partial update: nil fields are left alone. To clear a
// nullable field, pass a typed sql.NullX with Valid=false.
type TaskPatch struct {
	Title                 *string
	Notes                 *sql.NullString
	ProjectID             *sql.NullString
	IsImportant           *bool
	DueDate               *sql.NullTime
	DueTime               *sql.NullString
	ReminderAt            *sql.NullTime
	ReminderOffsetMinutes *sql.NullInt64
}

func (t *Tasks) Update(ctx context.Context, userID, id string, p TaskPatch) (*models.Task, error) {
	sets := []string{}
	args := []any{}

	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return nil, errors.New("services: title cannot be empty")
		}
		sets = append(sets, "title = ?")
		args = append(args, title)
	}
	if p.Notes != nil {
		// Recompute notes_html from the new source. An empty/cleared notes
		// value yields a NULL html column too, keeping the two in sync.
		var html sql.NullString
		if p.Notes.Valid {
			rendered, err := renderNotesHTML(p.Notes.String)
			if err != nil {
				return nil, err
			}
			html = rendered
		}
		sets = append(sets, "notes = ?", "notes_html = ?")
		args = append(args, *p.Notes, html)
	}
	if p.ProjectID != nil {
		sets = append(sets, "project_id = ?")
		args = append(args, *p.ProjectID)
	}
	if p.IsImportant != nil {
		sets = append(sets, "is_important = ?")
		args = append(args, *p.IsImportant)
	}
	if p.DueDate != nil {
		sets = append(sets, "due_date = ?")
		args = append(args, *p.DueDate)
		// Clearing the date implicitly clears the time AND the reminder.
		// A reminder is meaningless without a due datetime to anchor against.
		if !p.DueDate.Valid {
			sets = append(sets, "due_time = NULL",
				"reminder_at = NULL", "reminder_offset_minutes = NULL",
				"reminder_sent_at = NULL")
		}
	}
	if p.DueTime != nil {
		sets = append(sets, "due_time = ?")
		args = append(args, *p.DueTime)
		// Clearing the time also clears the reminder.
		if !p.DueTime.Valid {
			sets = append(sets, "reminder_at = NULL",
				"reminder_offset_minutes = NULL", "reminder_sent_at = NULL")
		}
	}
	if p.ReminderAt != nil {
		// Reset the delivered marker when the reminder changes, so a freshly
		// scheduled (or rescheduled) reminder fires once polling catches up.
		sets = append(sets, "reminder_at = ?", "reminder_sent_at = NULL")
		args = append(args, *p.ReminderAt)
	}
	if p.ReminderOffsetMinutes != nil {
		sets = append(sets, "reminder_offset_minutes = ?")
		args = append(args, *p.ReminderOffsetMinutes)
	}

	if len(sets) == 0 {
		return t.Get(ctx, userID, id)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, t.now())
	args = append(args, id, userID)

	res, err := t.db.ExecContext(ctx,
		`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTaskNotFound
	}
	return t.Get(ctx, userID, id)
}

// ----------------------------------------------------------- Complete/Uncomplete ---

// Complete sets completed_at to "now" and returns the updated task plus the
// next instance if the task has a recurrence rule attached. The "generated"
// return is nil when no regeneration happened (no rule, or this call was a
// no-op against an already-completed task).
//
// On the active→completed transition, if the task has a recurrence rule
// attached, the next instance is generated from the rule's template fields
// with a freshly-computed due date. Repeated calls are no-ops for
// regeneration; only the first one fires it.
func (t *Tasks) Complete(ctx context.Context, userID, id string) (completed, generated *models.Task, err error) {
	existing, err := t.Get(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}

	now := t.now()
	res, err := t.db.ExecContext(ctx, `
        UPDATE tasks
           SET completed_at = ?,
               updated_at = ?
         WHERE id = ? AND user_id = ? AND completed_at IS NULL`,
		now, now, id, userID,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("complete task: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Already completed (Get above proved the task exists). Idempotent
		// no-op — return current state without regenerating.
		return existing, nil, nil
	}

	completed, err = t.Get(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}

	if completed.RecurrenceRuleID.Valid {
		generated, err = t.regenerateNext(ctx, completed)
		if err != nil {
			return nil, nil, err
		}
	}
	return completed, generated, nil
}

// Uncomplete clears completed_at, restoring the task to the active lists.
func (t *Tasks) Uncomplete(ctx context.Context, userID, id string) (*models.Task, error) {
	res, err := t.db.ExecContext(ctx, `
        UPDATE tasks
           SET completed_at = NULL,
               updated_at = ?
         WHERE id = ? AND user_id = ?`,
		t.now(), id, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("uncomplete task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTaskNotFound
	}
	return t.Get(ctx, userID, id)
}

// ----------------------------------------------------------- Reminders ---

// DueReminder is the slim shape returned by /api/reminders/due. We send only
// what the browser needs for the notification body, not the full task row.
type DueReminder struct {
	ID    string
	Title string
	Notes string // plaintext source — browser notifications don't render HTML
}

// PopDueReminders atomically returns and marks-as-delivered the user's
// undelivered reminders whose reminder_at is in the past. The marking step
// uses a single UPDATE…RETURNING so two concurrent polls can't both deliver
// the same row.
func (t *Tasks) PopDueReminders(ctx context.Context, userID string) ([]DueReminder, error) {
	now := t.now()
	rows, err := t.db.QueryContext(ctx, `
        UPDATE tasks
           SET reminder_sent_at = ?
         WHERE user_id = ?
           AND reminder_at IS NOT NULL
           AND reminder_at <= ?
           AND reminder_sent_at IS NULL
           AND completed_at IS NULL
        RETURNING id, title, notes`,
		now, userID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("pop due reminders: %w", err)
	}
	defer rows.Close()

	var out []DueReminder
	for rows.Next() {
		var (
			id    string
			title string
			notes sql.NullString
		)
		if err := rows.Scan(&id, &title, &notes); err != nil {
			return nil, fmt.Errorf("scan due reminder: %w", err)
		}
		out = append(out, DueReminder{ID: id, Title: title, Notes: notes.String})
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------- Delete ---

func (t *Tasks) Delete(ctx context.Context, userID, id string) error {
	res, err := t.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// --------------------------------------------------------------- Helpers ---

// taskSelect joins projects so list rows can show their project's name + icon
// without a second round-trip. The two trailing columns are nullable —
// LEFT JOIN means tasks without a project come back with NULL/NULL there.
const taskSelect = `
SELECT t.id, t.user_id, t.project_id, t.title, t.notes, t.notes_html,
       t.is_important, t.due_date, t.due_time,
       t.reminder_at, t.reminder_offset_minutes, t.reminder_sent_at,
       t.completed_at, t.position, t.recurrence_rule_id, t.created_at, t.updated_at,
       p.name, p.icon
  FROM tasks t
  LEFT JOIN projects p ON p.id = t.project_id`

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*models.Task, error) {
	var t models.Task
	err := s.Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.Notes, &t.NotesHTML,
		&t.IsImportant, &t.DueDate, &t.DueTime,
		&t.ReminderAt, &t.ReminderOffsetMinutes, &t.ReminderSentAt,
		&t.CompletedAt, &t.Position, &t.RecurrenceRuleID, &t.CreatedAt, &t.UpdatedAt,
		&t.ProjectName, &t.ProjectIcon,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}
	return &t, nil
}
