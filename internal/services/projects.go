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
	ErrProjectNotFound = errors.New("services: project not found")
)

// MoveDirection is the argument to Projects.Move.
type MoveDirection string

const (
	MoveUp   MoveDirection = "up"
	MoveDown MoveDirection = "down"
)

type Projects struct {
	db  *sql.DB
	now func() time.Time
}

func NewProjects(db *sql.DB) *Projects {
	return &Projects{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// ----------------------------------------------------------------- Create ---

type CreateProjectParams struct {
	Name string
	Icon string // emoji, optional
}

func (p *Projects) Create(ctx context.Context, userID string, in CreateProjectParams) (*models.Project, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("services: project name is required")
	}

	now := p.now()
	pr := &models.Project{
		ID:        uuid.NewString(),
		UserID:    userID,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if icon := strings.TrimSpace(in.Icon); icon != "" {
		pr.Icon = sql.NullString{String: icon, Valid: true}
	}

	_, err := p.db.ExecContext(ctx, `
        INSERT INTO projects (id, user_id, name, icon, color, position, created_at, updated_at)
        VALUES (?, ?, ?, ?, NULL,
            COALESCE((SELECT MAX(position) FROM projects WHERE user_id = ?), 0) + 1,
            ?, ?)
    `,
		pr.ID, pr.UserID, pr.Name, pr.Icon,
		userID,
		pr.CreatedAt, pr.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}

	return p.Get(ctx, userID, pr.ID)
}

// ------------------------------------------------------------------- Get ---

func (p *Projects) Get(ctx context.Context, userID, id string) (*models.Project, error) {
	row := p.db.QueryRowContext(ctx, projectSelect+` WHERE id = ? AND user_id = ?`, id, userID)
	return scanProject(row)
}

// ------------------------------------------------------------------- List ---

func (p *Projects) List(ctx context.Context, userID string) ([]models.Project, error) {
	rows, err := p.db.QueryContext(ctx, projectSelect+` WHERE user_id = ? ORDER BY position`, userID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []models.Project
	for rows.Next() {
		pr, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pr)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------- Update ---

type ProjectPatch struct {
	Name *string
	Icon *sql.NullString
}

func (p *Projects) Update(ctx context.Context, userID, id string, patch ProjectPatch) (*models.Project, error) {
	sets := []string{}
	args := []any{}

	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, errors.New("services: project name cannot be empty")
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if patch.Icon != nil {
		sets = append(sets, "icon = ?")
		args = append(args, *patch.Icon)
	}

	if len(sets) == 0 {
		return p.Get(ctx, userID, id)
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, p.now())
	args = append(args, id, userID)

	res, err := p.db.ExecContext(ctx,
		`UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrProjectNotFound
	}
	return p.Get(ctx, userID, id)
}

// ----------------------------------------------------------------- Delete ---

// Delete removes the project. Tasks fall back to Inbox via the schema's
// ON DELETE SET NULL on tasks.project_id.
func (p *Projects) Delete(ctx context.Context, userID, id string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProjectNotFound
	}
	return nil
}

// ------------------------------------------------------------------- Move ---

// Move swaps a project's position with its immediate neighbour in the given
// direction. No-op if the project is already at the edge.
func (p *Projects) Move(ctx context.Context, userID, id string, dir MoveDirection) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var pos int64
	if err := tx.QueryRowContext(ctx,
		`SELECT position FROM projects WHERE id = ? AND user_id = ?`, id, userID,
	).Scan(&pos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		}
		return fmt.Errorf("read position: %w", err)
	}

	var neighbourQuery string
	switch dir {
	case MoveUp:
		neighbourQuery = `SELECT id, position FROM projects WHERE user_id = ? AND position < ? ORDER BY position DESC LIMIT 1`
	case MoveDown:
		neighbourQuery = `SELECT id, position FROM projects WHERE user_id = ? AND position > ? ORDER BY position ASC LIMIT 1`
	default:
		return fmt.Errorf("services: invalid move direction %q", dir)
	}

	var nID string
	var nPos int64
	if err := tx.QueryRowContext(ctx, neighbourQuery, userID, pos).Scan(&nID, &nPos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Already at the edge; nothing to do.
			return tx.Commit()
		}
		return fmt.Errorf("read neighbour: %w", err)
	}

	now := p.now()
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET position = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		nPos, now, id, userID,
	); err != nil {
		return fmt.Errorf("swap self: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET position = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		pos, now, nID, userID,
	); err != nil {
		return fmt.Errorf("swap neighbour: %w", err)
	}

	return tx.Commit()
}

// ----------------------------------------------------------------- Reorder ---

// Reorder rewrites the position column for the given project IDs so they end
// up in the order the slice presents them. Same strategy as Tasks.Reorder:
// reuse the existing position values, just permute their assignment.
func (p *Projects) Reorder(ctx context.Context, userID string, ids []string) error {
	if len(ids) < 2 {
		return nil
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	type entry struct {
		id  string
		pos int64
	}
	rows := make([]entry, 0, len(ids))
	for _, id := range ids {
		var pos int64
		err := tx.QueryRowContext(ctx,
			`SELECT position FROM projects WHERE id = ? AND user_id = ?`, id, userID,
		).Scan(&pos)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read position for %s: %w", id, err)
		}
		rows = append(rows, entry{id: id, pos: pos})
	}
	if len(rows) < 2 {
		return tx.Commit()
	}

	positions := make([]int64, len(rows))
	for i, e := range rows {
		positions[i] = e.pos
	}
	for i := 1; i < len(positions); i++ {
		for j := i; j > 0 && positions[j-1] > positions[j]; j-- {
			positions[j-1], positions[j] = positions[j], positions[j-1]
		}
	}

	now := p.now()
	for i, e := range rows {
		if _, err := tx.ExecContext(ctx,
			`UPDATE projects SET position = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
			positions[i], now, e.id, userID,
		); err != nil {
			return fmt.Errorf("write position for %s: %w", e.id, err)
		}
	}
	return tx.Commit()
}

// --------------------------------------------------------------- Helpers ---

const projectSelect = `
SELECT id, user_id, name, icon, color, position, created_at, updated_at
  FROM projects`

func scanProject(s scanner) (*models.Project, error) {
	var p models.Project
	err := s.Scan(
		&p.ID, &p.UserID, &p.Name, &p.Icon, &p.Color, &p.Position,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	return &p, nil
}
