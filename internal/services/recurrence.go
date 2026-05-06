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

var ErrRuleNotFound = errors.New("services: recurrence rule not found")

// RecurrenceParams is the input shape for create/update of a task's
// recurrence rule. Frequency == "" means "no recurrence" — used by
// SetRecurrenceForTask to remove an existing rule.
type RecurrenceParams struct {
	Frequency        string
	Interval         int
	RegenerationType string
}

// normalize fills defaults and validates. Returns an error for unknown
// frequencies / regeneration types so the caller surfaces a 400.
func (p *RecurrenceParams) normalize() error {
	p.Frequency = strings.ToLower(strings.TrimSpace(p.Frequency))
	switch p.Frequency {
	case "days", "weeks", "months", "years":
	default:
		return fmt.Errorf("recurrence frequency must be days/weeks/months/years, got %q", p.Frequency)
	}
	if p.Interval <= 0 {
		p.Interval = 1
	}
	p.RegenerationType = strings.ToLower(strings.TrimSpace(p.RegenerationType))
	switch p.RegenerationType {
	case "on_create", "on_complete":
	case "":
		p.RegenerationType = "on_create"
	default:
		return fmt.Errorf("recurrence type must be on_create or on_complete, got %q", p.RegenerationType)
	}
	return nil
}

// SetRecurrenceForTask is the unified entry point used by the handler form.
// Behaviour:
//
//   - p.Frequency == ""     → remove any existing rule
//   - rule already attached → update its fields in place
//   - no rule yet           → create one and attach to the task
//
// The task itself is the source of truth for the rule's template_* fields:
// title / notes / project / is_important / due_time get copied from the
// task on every set so future instances inherit "the task as it currently
// looks".
func (t *Tasks) SetRecurrenceForTask(ctx context.Context, userID, taskID string, p RecurrenceParams) error {
	task, err := t.Get(ctx, userID, taskID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(p.Frequency) == "" {
		if !task.RecurrenceRuleID.Valid {
			return nil
		}
		return t.unsetRule(ctx, userID, task)
	}

	if err := p.normalize(); err != nil {
		return err
	}

	if task.RecurrenceRuleID.Valid {
		return t.updateRule(ctx, userID, task, p)
	}
	return t.createRuleForTask(ctx, userID, task, p)
}

func (t *Tasks) createRuleForTask(ctx context.Context, userID string, task *models.Task, p RecurrenceParams) error {
	ruleID := uuid.NewString()
	now := t.now()
	_, err := t.db.ExecContext(ctx, `
        INSERT INTO recurrence_rules (
            id, user_id, frequency, interval, regeneration_type,
            template_title, template_notes, template_project_id,
            template_is_important, template_due_time, template_reminder_offset,
            created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		ruleID, userID, p.Frequency, p.Interval, p.RegenerationType,
		task.Title, task.Notes, task.ProjectID,
		task.IsImportant, task.DueTime, now,
	)
	if err != nil {
		return fmt.Errorf("create recurrence rule: %w", err)
	}
	if _, err := t.db.ExecContext(ctx,
		`UPDATE tasks SET recurrence_rule_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		ruleID, now, task.ID, userID,
	); err != nil {
		return fmt.Errorf("link task to recurrence rule: %w", err)
	}
	return nil
}

func (t *Tasks) updateRule(ctx context.Context, userID string, task *models.Task, p RecurrenceParams) error {
	_, err := t.db.ExecContext(ctx, `
        UPDATE recurrence_rules
           SET frequency = ?, interval = ?, regeneration_type = ?,
               template_title = ?, template_notes = ?, template_project_id = ?,
               template_is_important = ?, template_due_time = ?
         WHERE id = ? AND user_id = ?`,
		p.Frequency, p.Interval, p.RegenerationType,
		task.Title, task.Notes, task.ProjectID,
		task.IsImportant, task.DueTime,
		task.RecurrenceRuleID.String, userID,
	)
	if err != nil {
		return fmt.Errorf("update recurrence rule: %w", err)
	}
	return nil
}

// unsetRule clears the task's pointer to its rule and deletes the rule row
// when no other task references it. Each chain only ever has one active
// instance at a time (next is generated on completion), so in practice this
// always cleans up — the COUNT guard is just defensive.
func (t *Tasks) unsetRule(ctx context.Context, userID string, task *models.Task) error {
	if !task.RecurrenceRuleID.Valid {
		return nil
	}
	ruleID := task.RecurrenceRuleID.String

	if _, err := t.db.ExecContext(ctx,
		`UPDATE tasks SET recurrence_rule_id = NULL, updated_at = ? WHERE id = ? AND user_id = ?`,
		t.now(), task.ID, userID,
	); err != nil {
		return fmt.Errorf("clear task recurrence pointer: %w", err)
	}

	var refs int
	if err := t.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE recurrence_rule_id = ? AND user_id = ?`,
		ruleID, userID,
	).Scan(&refs); err != nil {
		return fmt.Errorf("count rule references: %w", err)
	}
	if refs == 0 {
		if _, err := t.db.ExecContext(ctx,
			`DELETE FROM recurrence_rules WHERE id = ? AND user_id = ?`,
			ruleID, userID,
		); err != nil {
			return fmt.Errorf("delete orphan recurrence rule: %w", err)
		}
	}
	return nil
}

// GetRule fetches a rule by id. Used by handlers to render the detail panel
// with the current rule's values pre-selected.
func (t *Tasks) GetRule(ctx context.Context, userID, ruleID string) (*models.RecurrenceRule, error) {
	row := t.db.QueryRowContext(ctx, `
        SELECT id, user_id, frequency, interval, regeneration_type,
               template_title, template_notes, template_project_id,
               template_is_important, template_due_time, template_reminder_offset,
               created_at
          FROM recurrence_rules
         WHERE id = ? AND user_id = ?`,
		ruleID, userID,
	)
	var r models.RecurrenceRule
	err := row.Scan(
		&r.ID, &r.UserID, &r.Frequency, &r.Interval, &r.RegenerationType,
		&r.TemplateTitle, &r.TemplateNotes, &r.TemplateProjectID,
		&r.TemplateIsImportant, &r.TemplateDueTime, &r.TemplateReminderOffset,
		&r.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get recurrence rule: %w", err)
	}
	return &r, nil
}

// regenerateNext inserts the next instance of a recurring task. It's only
// called by Complete (after the active→completed transition) so callers
// don't need to worry about double-firing. Returns the new task or nil if
// the rule is gone (defensive — can't happen in normal use).
func (t *Tasks) regenerateNext(ctx context.Context, prev *models.Task) (*models.Task, error) {
	if !prev.RecurrenceRuleID.Valid {
		return nil, nil
	}
	rule, err := t.GetRule(ctx, prev.UserID, prev.RecurrenceRuleID.String)
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) {
			return nil, nil
		}
		return nil, err
	}

	nextDue := computeNextDueDate(rule, prev, t.now())

	id := uuid.NewString()
	now := t.now()

	notes := rule.TemplateNotes
	var notesHTML sql.NullString
	if notes.Valid {
		if html, err := renderNotesHTML(notes.String); err == nil {
			notesHTML = html
		}
	}

	_, err = t.db.ExecContext(ctx, `
        INSERT INTO tasks (
            id, user_id, project_id, title, notes, notes_html,
            is_important, due_date, due_time,
            position, recurrence_rule_id, created_at, updated_at
        ) VALUES (
            ?, ?, ?, ?, ?, ?,
            ?, ?, ?,
            COALESCE((SELECT MAX(position) FROM tasks WHERE user_id = ?), 0) + 1,
            ?, ?, ?
        )`,
		id, prev.UserID, rule.TemplateProjectID, rule.TemplateTitle, notes, notesHTML,
		rule.TemplateIsImportant, sql.NullTime{Time: nextDue, Valid: true}, rule.TemplateDueTime,
		prev.UserID,
		sql.NullString{String: rule.ID, Valid: true}, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert next recurring task: %w", err)
	}
	return t.Get(ctx, prev.UserID, id)
}

// computeNextDueDate is pure: given a rule, the previous task instance, and
// "now", return the new task's due date.
//
//	on_create   — anchor to the previous instance's due_date (fixed
//	              schedule). Falls back to now if the previous didn't have
//	              a date.
//	on_complete — anchor to now (relative to completion).
func computeNextDueDate(rule *models.RecurrenceRule, prev *models.Task, now time.Time) time.Time {
	var anchor time.Time
	switch rule.RegenerationType {
	case "on_complete":
		anchor = now
	default: // on_create
		if prev.DueDate.Valid {
			anchor = prev.DueDate.Time
		} else {
			anchor = now
		}
	}
	return addInterval(anchor, rule.Frequency, rule.Interval)
}

func addInterval(t time.Time, freq string, n int) time.Time {
	switch freq {
	case "days":
		return t.AddDate(0, 0, n)
	case "weeks":
		return t.AddDate(0, 0, n*7)
	case "months":
		return t.AddDate(0, n, 0)
	case "years":
		return t.AddDate(n, 0, 0)
	}
	return t
}
