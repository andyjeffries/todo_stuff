package middleware

import (
	"context"
	"net/http"
)

type ctxKey int

const themeCtxKey ctxKey = 0

const themeCookieName = "tt_theme"

// Theme reads ?theme=dark|light|auto and persists it in the tt_theme cookie,
// then redirects to the same URL with the theme param removed. On every
// request it reads the cookie value into the context. Empty / unrecognised
// cookie → empty string in ctx (auto).
func Theme(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Honour ?theme=… and redirect away.
		if v := r.URL.Query().Get("theme"); v != "" && r.Method == http.MethodGet {
			cookie := &http.Cookie{
				Name:     themeCookieName,
				Path:     "/",
				MaxAge:   60 * 60 * 24 * 365, // 1y
				HttpOnly: false,              // not security-sensitive; readable from JS is fine
				SameSite: http.SameSiteLaxMode,
			}
			switch v {
			case "dark", "light":
				cookie.Value = v
			case "auto":
				cookie.MaxAge = -1 // delete
			default:
				// unknown — strip the param without touching the cookie
			}
			if cookie.Value != "" || cookie.MaxAge < 0 {
				http.SetCookie(w, cookie)
			}
			q := r.URL.Query()
			q.Del("theme")
			target := r.URL.Path
			if enc := q.Encode(); enc != "" {
				target += "?" + enc
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		// 2. Read cookie → ctx.
		theme := ""
		if c, err := r.Cookie(themeCookieName); err == nil {
			if c.Value == "dark" || c.Value == "light" {
				theme = c.Value
			}
		}
		ctx := context.WithValue(r.Context(), themeCtxKey, theme)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ThemeFromContext returns "", "light", or "dark".
func ThemeFromContext(ctx context.Context) string {
	v, _ := ctx.Value(themeCtxKey).(string)
	return v
}
