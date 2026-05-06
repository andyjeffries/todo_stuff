package auth

import (
	"context"

	"github.com/andyjessop/todostuff/internal/models"
)

type ctxKey int

const (
	userCtxKey ctxKey = iota
	sessionCtxKey
)

func WithUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

func UserFromContext(ctx context.Context) (*models.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*models.User)
	return u, ok
}

func WithSession(ctx context.Context, s *models.Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey, s)
}

func SessionFromContext(ctx context.Context) (*models.Session, bool) {
	s, ok := ctx.Value(sessionCtxKey).(*models.Session)
	return s, ok
}
