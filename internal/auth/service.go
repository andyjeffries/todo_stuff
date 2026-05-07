package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
	"github.com/google/uuid"
)

const (
	SessionCookieName = "todostuff_session"
	SessionDuration   = 30 * 24 * time.Hour
)

var (
	ErrNotFound          = errors.New("auth: not found")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrEmailTaken        = errors.New("auth: email already in use")
)

// Service combines user + session persistence and the cookie wiring used by
// HTTP handlers. It is safe for concurrent use.
type Service struct {
	db           *sql.DB
	cookieSecure bool
}

func NewService(db *sql.DB, cookieSecure bool) *Service {
	return &Service{db: db, cookieSecure: cookieSecure}
}

// ----------------------------------------------------------------- Users ---

func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

type CreateUserParams struct {
	Email    string
	Password string
	Name     string
	IsAdmin  bool
}

func (s *Service) CreateUser(ctx context.Context, p CreateUserParams) (*models.User, error) {
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if email == "" || p.Password == "" || strings.TrimSpace(p.Name) == "" {
		return nil, errors.New("auth: email, password and name are required")
	}

	hash, err := HashPassword(p.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	now := time.Now().UTC()
	u := &models.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: hash,
		Name:         strings.TrimSpace(p.Name),
		IsAdmin:      p.IsAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	_, err = s.db.ExecContext(ctx, `
        INSERT INTO users (id, email, password_hash, name, is_admin, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, u.ID, u.Email, u.PasswordHash, u.Name, u.IsAdmin, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// UpdateProfileParams collects the editable profile fields. Email is not
// here on purpose — changing email is an admin operation in this product
// and out of scope for /profile.
type UpdateProfileParams struct {
	Name            string
	PushoverUserKey string // empty string = clear the key
	PushoverEnabled bool
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, p UpdateProfileParams) error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errors.New("auth: name is required")
	}
	key := strings.TrimSpace(p.PushoverUserKey)
	// An empty key means "no Pushover" — also force enabled=0 so the dispatcher
	// can rely on `enabled=1 AND key IS NOT NULL` without an extra null check.
	enabled := p.PushoverEnabled
	if key == "" {
		enabled = false
	}

	var keyArg any
	if key == "" {
		keyArg = nil
	} else {
		keyArg = key
	}

	res, err := s.db.ExecContext(ctx, `
        UPDATE users
           SET name = ?,
               pushover_user_key = ?,
               pushover_enabled = ?,
               updated_at = ?
         WHERE id = ?
    `, name, keyArg, enabled, time.Now().UTC(), userID)
	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword verifies the current password and replaces it. Same
// constant-time-ish bcrypt compare semantics as Authenticate — wrong current
// password returns ErrInvalidCredentials.
func (s *Service) UpdatePassword(ctx context.Context, userID, current, next string) error {
	u, err := s.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !CheckPassword(u.PasswordHash, current) {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(next)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
        UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?
    `, hash, time.Now().UTC(), userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

func (s *Service) FindUserByEmail(ctx context.Context, email string) (*models.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.queryUser(ctx, `WHERE email = ?`, email)
}

func (s *Service) FindUserByID(ctx context.Context, id string) (*models.User, error) {
	return s.queryUser(ctx, `WHERE id = ?`, id)
}

func (s *Service) queryUser(ctx context.Context, where string, args ...any) (*models.User, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, email, password_hash, name, is_admin,
               pushover_user_key, pushover_enabled, created_at, updated_at
          FROM users `+where, args...)
	var u models.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsAdmin,
		&u.PushoverUserKey, &u.PushoverEnabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &u, nil
}

// Authenticate returns the user if email + password match. It always performs
// a bcrypt comparison (even when the user doesn't exist) to keep the response
// time independent of email existence.
func (s *Service) Authenticate(ctx context.Context, email, password string) (*models.User, error) {
	u, err := s.FindUserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		// Run a dummy comparison so non-existent users take similar time as bad passwords.
		_ = CheckPassword("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidi", password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// -------------------------------------------------------------- Sessions ---

func (s *Service) CreateSession(ctx context.Context, userID string) (*models.Session, error) {
	tok, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	now := time.Now().UTC()
	sess := &models.Session{
		ID:        tok,
		UserID:    userID,
		ExpiresAt: now.Add(SessionDuration),
		CreatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO sessions (id, user_id, expires_at, created_at)
        VALUES (?, ?, ?, ?)
    `, sess.ID, sess.UserID, sess.ExpiresAt, sess.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return sess, nil
}

// LookupSession returns the session and its owning user for a given token.
// Expired sessions are deleted on the spot and reported as ErrNotFound.
func (s *Service) LookupSession(ctx context.Context, token string) (*models.Session, *models.User, error) {
	if token == "" {
		return nil, nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
        SELECT s.id, s.user_id, s.expires_at, s.created_at,
               u.id, u.email, u.password_hash, u.name, u.is_admin,
               u.pushover_user_key, u.pushover_enabled, u.created_at, u.updated_at
          FROM sessions s
          JOIN users u ON u.id = s.user_id
         WHERE s.id = ?
    `, token)

	var sess models.Session
	var u models.User
	err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt,
		&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsAdmin,
		&u.PushoverUserKey, &u.PushoverEnabled, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("lookup session: %w", err)
	}
	if sess.IsExpired(time.Now().UTC()) {
		_ = s.DeleteSession(ctx, sess.ID)
		return nil, nil, ErrNotFound
	}
	return &sess, &u, nil
}

func (s *Service) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Service) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	return nil
}

// --------------------------------------------------------------- Cookies ---

func (s *Service) SetSessionCookie(w http.ResponseWriter, sess *models.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// LoadSessionFromRequest reads the session cookie and returns the matching
// session + user, or (nil, nil, nil) if no/invalid cookie is present. A
// non-nil error is reserved for storage failures.
func (s *Service) LoadSessionFromRequest(r *http.Request) (*models.Session, *models.User, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, nil, nil
	}
	sess, u, err := s.LookupSession(r.Context(), cookie.Value)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return sess, u, nil
}

// ---------------------------------------------------------------- Helpers ---

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// Kept as a string check to avoid pulling sqlite3 error types into this package.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
