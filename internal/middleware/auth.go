// Package middleware contains HTTP middleware specific to the application.
package middleware

import (
	"log/slog"
	"net/http"

	"github.com/andyjessop/todostuff/internal/auth"
)

// RequireAuth ensures the request carries a valid session cookie. Authenticated
// requests get the user + session attached to the context; unauthenticated
// ones are redirected to /login.
func RequireAuth(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, user, err := svc.LoadSessionFromRequest(r)
			if err != nil {
				slog.Error("session lookup failed", "err", err, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if user == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			ctx := auth.WithUser(r.Context(), user)
			ctx = auth.WithSession(ctx, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
