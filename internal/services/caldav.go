package services

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
)

// CalDAVCompletedWindow bounds how much of the Logbook a CalDAV client sees:
// completed tasks older than this drop out of the lists, so a phone's "Show
// Completed" stays small rather than downloading years of history.
const CalDAVCompletedWindow = 30 * 24 * time.Hour

// Tasks with no project are split over two CalDAV collections, mirroring
// the smart lists: Inbox (no date) and Upcoming (dated). Every other
// collection is a project, named by its id.
const (
	CalDAVInbox    = "inbox"
	CalDAVUpcoming = "upcoming"
)

// CalDAVCollectionOf is the collection a task is listed in.
func CalDAVCollectionOf(t *models.Task) string {
	switch {
	case t.ProjectID.Valid:
		return t.ProjectID.String
	case t.DueDate.Valid:
		return CalDAVUpcoming
	}
	return CalDAVInbox
}

// IsCalDAVProjectless reports whether coll is one of the two no-project
// collections. The server moves tasks between them on its own when their
// date is set or cleared.
func IsCalDAVProjectless(coll string) bool {
	return coll == CalDAVInbox || coll == CalDAVUpcoming
}

// collectionWhere scopes tasks to one CalDAV collection.
func collectionWhere(coll string) (string, []any) {
	switch coll {
	case CalDAVInbox:
		return `t.project_id IS NULL AND t.due_date IS NULL`, nil
	case CalDAVUpcoming:
		return `t.project_id IS NULL AND t.due_date IS NOT NULL`, nil
	}
	return `t.project_id = ?`, []any{coll}
}

// ListForCalDAV returns a collection's open tasks plus the ones completed
// within CalDAVCompletedWindow.
func (t *Tasks) ListForCalDAV(ctx context.Context, userID, coll string) ([]models.Task, error) {
	where, args := collectionWhere(coll)
	args = append([]any{userID}, args...)
	args = append(args, t.now().Add(-CalDAVCompletedWindow))
	rows, err := t.db.QueryContext(ctx, taskSelect+`
         WHERE t.user_id = ? AND `+where+`
           AND (t.completed_at IS NULL OR t.completed_at >= ?)
         ORDER BY t.position`, args...)
	if err != nil {
		return nil, fmt.Errorf("list caldav tasks: %w", err)
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

// CollectionCTag is the CalendarServer getctag for one collection: it changes
// whenever a task in it is added, edited, completed, moved out or deleted
// (count catches the last two), or the project itself is renamed.
func (t *Tasks) CollectionCTag(ctx context.Context, userID, coll string) (string, error) {
	where, args := collectionWhere(coll)
	args = append([]any{userID}, args...)
	var raw string
	err := t.db.QueryRowContext(ctx, `
        SELECT COUNT(*) || ':' || COALESCE(MAX(t.updated_at), '')
          FROM tasks t WHERE t.user_id = ? AND `+where, args...).Scan(&raw)
	if err != nil {
		return "", fmt.Errorf("collection ctag: %w", err)
	}
	if !IsCalDAVProjectless(coll) {
		var updated sql.NullString
		err := t.db.QueryRowContext(ctx,
			`SELECT updated_at FROM projects WHERE id = ? AND user_id = ?`, coll, userID,
		).Scan(&updated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("collection ctag project: %w", err)
		}
		raw += "|" + updated.String
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16]), nil
}

// GetByCalDAVName finds a task by its resource name, in any collection.
func (t *Tasks) GetByCalDAVName(ctx context.Context, userID, name string) (*models.Task, error) {
	row := t.db.QueryRowContext(ctx, taskSelect+`
         WHERE t.user_id = ?
           AND (t.ical_href = ? OR (t.ical_href IS NULL AND t.id || '.ics' = ?))`,
		userID, name, name)
	return scanTask(row)
}

// GetByCalDAVUID finds a task by the iCalendar UID a client knows it by.
func (t *Tasks) GetByCalDAVUID(ctx context.Context, userID, uid string) (*models.Task, error) {
	row := t.db.QueryRowContext(ctx, taskSelect+`
         WHERE t.user_id = ?
           AND (t.ical_uid = ? OR (t.ical_uid IS NULL AND t.id = ?))`,
		userID, uid, uid)
	return scanTask(row)
}

// SetCalDAVMeta records the UID, resource name and unmodelled properties a
// CalDAV client wrote. Values equal to the defaults (UID = id, name =
// id.ics) are stored as NULL.
func (t *Tasks) SetCalDAVMeta(ctx context.Context, userID, id, uid, name, extra string) error {
	nullIf := func(v, def string) sql.NullString {
		if v == "" || v == def {
			return sql.NullString{}
		}
		return sql.NullString{String: v, Valid: true}
	}
	res, err := t.db.ExecContext(ctx, `
        UPDATE tasks SET ical_uid = ?, ical_href = ?, ical_extra = ?, updated_at = ?
         WHERE id = ? AND user_id = ?`,
		nullIf(uid, id), nullIf(name, id+".ics"), nullIf(extra, ""), t.now(), id, userID)
	if err != nil {
		return fmt.Errorf("set caldav meta: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTaskNotFound
	}
	return nil
}
