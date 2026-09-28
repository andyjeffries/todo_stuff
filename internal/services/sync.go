package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SyncToken returns an opaque fingerprint of everything a client renders for
// the user: tasks, projects, and their own profile. It changes whenever any
// of those rows is created, updated, reordered, completed, or deleted, so
// API clients can poll it cheaply and only refetch lists when it moves.
//
// Row count catches deletes (which leave no updated_at behind); MAX(updated_at)
// catches every other write, since all mutations bump that column.
func (t *Tasks) SyncToken(ctx context.Context, userID string) (string, error) {
	var raw string
	err := t.db.QueryRowContext(ctx, `
        SELECT (SELECT COUNT(*) || ':' || COALESCE(MAX(updated_at), '') FROM tasks WHERE user_id = ?)
            || '|' ||
               (SELECT COUNT(*) || ':' || COALESCE(MAX(updated_at), '') FROM projects WHERE user_id = ?)
            || '|' ||
               COALESCE((SELECT updated_at FROM users WHERE id = ?), '')
    `, userID, userID, userID).Scan(&raw)
	if err != nil {
		return "", fmt.Errorf("sync token: %w", err)
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16]), nil
}
