// Package services holds the business-logic layer for projects, tasks,
// recurrence, and notifications. Services are user-scoped: every operation
// takes a userID and refuses to touch rows owned by anyone else.
package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
	"github.com/google/uuid"
)

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
            id, user_id, project_id, title, notes,
            is_important, due_date, due_time,
            position, created_at, updated_at
        ) VALUES (
            ?, ?, ?, ?, ?,
            ?, ?, ?,
            COALESCE((SELECT MAX(position) FROM tasks WHERE user_id = ?), 0) + 1,
            ?, ?
        )
    `,
		task.ID, task.UserID, task.ProjectID, task.Title, task.Notes,
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
	row := t.db.QueryRowContext(ctx, taskSelect+` WHERE id = ? AND user_id = ?`, id, userID)
	return scanTask(row)
}

// ------------------------------------------------------------------- List ---

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
//
// Inbox semantics deliberately diverge from the master plan: an Inbox task is
// one with neither a project nor a due date — i.e. truly unprocessed work.
// Once you give a task a project or a date you've already filed it.
func viewQuery(userID string, view View, now time.Time) (where, order string, args []any) {
	today := now.Format("2006-01-02")
	switch view {
	case ViewToday:
		return `user_id = ? AND completed_at IS NULL AND (due_date IS NULL OR due_date <= ?)`,
			`due_time IS NULL, due_time, position`,
			[]any{userID, today}
	case ViewInbox:
		return `user_id = ? AND completed_at IS NULL AND project_id IS NULL AND due_date IS NULL`,
			`position`,
			[]any{userID}
	case ViewUpcoming:
		return `user_id = ? AND completed_at IS NULL AND due_date > ?`,
			`due_date, due_time IS NULL, due_time, position`,
			[]any{userID, today}
	case ViewLogbook:
		return `user_id = ? AND completed_at IS NOT NULL`,
			`completed_at DESC`,
			[]any{userID}
	default: // ViewAnytime
		return `user_id = ? AND completed_at IS NULL`,
			`project_id IS NULL, position`,
			[]any{userID}
	}
}

// ----------------------------------------------------------------- Update ---

// TaskPatch is a partial update: nil fields are left alone. To clear a
// nullable field, pass a typed sql.NullX with Valid=false.
type TaskPatch struct {
	Title       *string
	Notes       *sql.NullString
	ProjectID   *sql.NullString
	IsImportant *bool
	DueDate     *sql.NullTime
	DueTime     *sql.NullString
	ReminderAt  *sql.NullTime
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
		sets = append(sets, "notes = ?", "notes_html = NULL")
		args = append(args, *p.Notes)
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
		// Clearing the date implicitly clears the time.
		if !p.DueDate.Valid {
			sets = append(sets, "due_time = NULL")
		}
	}
	if p.DueTime != nil {
		sets = append(sets, "due_time = ?")
		args = append(args, *p.DueTime)
	}
	if p.ReminderAt != nil {
		sets = append(sets, "reminder_at = ?")
		args = append(args, *p.ReminderAt)
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

// Complete sets completed_at to "now" and returns the updated task.
// Idempotent: completing an already-completed task leaves the original
// completion timestamp in place.
func (t *Tasks) Complete(ctx context.Context, userID, id string) (*models.Task, error) {
	now := t.now()
	res, err := t.db.ExecContext(ctx, `
        UPDATE tasks
           SET completed_at = COALESCE(completed_at, ?),
               updated_at = ?
         WHERE id = ? AND user_id = ?`,
		now, now, id, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("complete task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTaskNotFound
	}
	return t.Get(ctx, userID, id)
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

const taskSelect = `
SELECT id, user_id, project_id, title, notes, notes_html,
       is_important, due_date, due_time, reminder_at, completed_at,
       position, recurrence_rule_id, created_at, updated_at
  FROM tasks`

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*models.Task, error) {
	var t models.Task
	err := s.Scan(
		&t.ID, &t.UserID, &t.ProjectID, &t.Title, &t.Notes, &t.NotesHTML,
		&t.IsImportant, &t.DueDate, &t.DueTime, &t.ReminderAt, &t.CompletedAt,
		&t.Position, &t.RecurrenceRuleID, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}
	return &t, nil
}
