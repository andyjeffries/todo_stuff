package handlers

import (
	"log/slog"
	"net/http"
)

// Root sends visitors to the right starting place:
//   - no users in the system → /setup (admin onboarding)
//   - already authenticated   → /today
//   - otherwise               → /login
func (h *Handlers) Root(w http.ResponseWriter, r *http.Request) {
	count, err := h.Auth.CountUsers(r.Context())
	if err != nil {
		slog.Error("count users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	if _, user, _ := h.Auth.LoadSessionFromRequest(r); user != nil {
		http.Redirect(w, r, "/today", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}
