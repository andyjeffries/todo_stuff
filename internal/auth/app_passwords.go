package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
	"github.com/google/uuid"
)

// AppPassword is a per-device credential for CalDAV, where the client can
// only do HTTP Basic auth. The plaintext is shown once, at creation.
type AppPassword struct {
	ID         string
	UserID     string
	Label      string
	LastUsedAt sql.NullTime
	CreatedAt  time.Time
}

// appPasswordAlphabet leaves out 0/o, 1/l/i so the password can be typed on
// a phone from another screen without guessing.
const appPasswordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newAppPassword returns 16 random characters in groups of four
// ("abcd-efgh-jkmn-pqrs"): ~79 bits, the dashes are cosmetic.
func newAppPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(appPasswordAlphabet[int(c)%len(appPasswordAlphabet)])
	}
	return sb.String(), nil
}

// hashAppPassword ignores dashes, spaces and case, so a hand-typed password
// still matches.
func hashAppPassword(pw string) string {
	pw = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(pw))
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])
}

// CreateAppPassword stores a new app password and returns it with its
// plaintext, which is never retrievable again.
func (s *Service) CreateAppPassword(ctx context.Context, userID, label string) (*AppPassword, string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, "", errors.New("auth: app password label is required")
	}
	pw, err := newAppPassword()
	if err != nil {
		return nil, "", fmt.Errorf("generate app password: %w", err)
	}
	u, err := s.FindUserByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	ap := &AppPassword{ID: uuid.NewString(), UserID: userID, Label: label, CreatedAt: time.Now().UTC()}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO app_passwords (id, user_id, label, token_hash, digest_ha1, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, ap.ID, ap.UserID, ap.Label, hashAppPassword(pw), digestHA1(u.Email, pw), ap.CreatedAt)
	if err != nil {
		return nil, "", fmt.Errorf("insert app password: %w", err)
	}
	return ap, pw, nil
}

func (s *Service) ListAppPasswords(ctx context.Context, userID string) ([]AppPassword, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, user_id, label, last_used_at, created_at
          FROM app_passwords WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list app passwords: %w", err)
	}
	defer rows.Close()
	var out []AppPassword
	for rows.Next() {
		var ap AppPassword
		if err := rows.Scan(&ap.ID, &ap.UserID, &ap.Label, &ap.LastUsedAt, &ap.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan app password: %w", err)
		}
		out = append(out, ap)
	}
	return out, rows.Err()
}

// DeleteAppPassword revokes one of the user's app passwords. ErrNotFound if
// it isn't theirs.
func (s *Service) DeleteAppPassword(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM app_passwords WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete app password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAppPassword returns the user if email + app password match
// (HTTP Basic). The account password is deliberately not accepted here.
func (s *Service) AuthenticateAppPassword(ctx context.Context, email, password string) (*models.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var apID string
	var userID string
	var lastUsed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
        SELECT ap.id, ap.user_id, ap.last_used_at
          FROM app_passwords ap
          JOIN users u ON u.id = ap.user_id
         WHERE ap.token_hash = ? AND u.email = ?`,
		hashAppPassword(password), email,
	).Scan(&apID, &userID, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("authenticate app password: %w", err)
	}
	s.touchAppPassword(ctx, apID, lastUsed)
	return s.FindUserByID(ctx, userID)
}

// touchAppPassword bumps last_used_at, at most once a minute.
func (s *Service) touchAppPassword(ctx context.Context, id string, lastUsed sql.NullTime) {
	now := time.Now().UTC()
	if !lastUsed.Valid || now.Sub(lastUsed.Time) > time.Minute {
		_, _ = s.db.ExecContext(ctx, `UPDATE app_passwords SET last_used_at = ? WHERE id = ?`, now, id)
	}
}
