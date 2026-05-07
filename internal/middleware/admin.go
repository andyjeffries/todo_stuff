package middleware

import (
	"net/http"

	"github.com/andyjessop/todostuff/internal/auth"
)

// RequireAdmin gates routes that mutate or expose user-management state. It
// must be stacked *after* RequireAuth — it relies on the user already living
// in the request context. Non-admins get a flat 403 (HTML) rather than a
// redirect: redirecting would mask programming mistakes (forgetting the
// gate) as harmless bouncing, and the admin URL space isn't linked from
// non-admin chrome anyway.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || !user.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
