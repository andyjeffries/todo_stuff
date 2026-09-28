package middleware

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
)

// RequireAPIAuth is the JSON-API counterpart to RequireAuth. It accepts a
// session token as `Authorization: Bearer <token>` (native clients) and falls
// back to the browser's session cookie, so the API can also be poked from a
// logged-in browser tab. Failures get a JSON 401 rather than a redirect.
//
// A Bearer header that's present but invalid is a hard 401 — we don't fall
// through to the cookie, so a client with a stale token finds out.
func RequireAPIAuth(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, user, err := lookupAPISession(svc, r)
			if err != nil {
				slog.Error("api session lookup failed", "err", err, "path", r.URL.Path)
				WriteJSONError(w, http.StatusInternalServerError, "internal_error", "Internal server error.")
				return
			}
			if user == nil {
				WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Missing, invalid, or expired session token.")
				return
			}
			ctx := auth.WithUser(r.Context(), user)
			ctx = auth.WithSession(ctx, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAPIAdmin is RequireAdmin with a JSON 403. Stack after RequireAPIAuth.
func RequireAPIAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || !user.IsAdmin {
			WriteJSONError(w, http.StatusForbidden, "forbidden", "Admin access required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// lookupAPISession returns (nil, nil, nil) for "no valid credentials"; a
// non-nil error is reserved for storage failures.
func lookupAPISession(svc *auth.Service, r *http.Request) (*models.Session, *models.User, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return svc.LoadSessionFromRequest(r)
	}
	scheme, token, _ := strings.Cut(h, " ")
	token = strings.TrimSpace(token)
	if !strings.EqualFold(scheme, "Bearer") || token == "" {
		return nil, nil, nil
	}
	sess, user, err := svc.LookupSession(r.Context(), token)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, nil, nil
	}
	return sess, user, err
}

// WriteJSONError writes the API's standard error envelope:
//
//	{"error": {"code": "not_found", "message": "Task not found."}}
func WriteJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
