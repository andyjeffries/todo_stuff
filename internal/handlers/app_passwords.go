package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/go-chi/chi/v5"
)

// App passwords are the CalDAV credentials (see dav.go): one per device, so
// a lost phone is revoked without touching anything else.

// ProfileAppPasswordCreate handles POST /profile/app-passwords.
func (h *Handlers) ProfileAppPasswordCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(r.PostFormValue("label"))
	if label == "" {
		h.renderProfile(w, r, "devices", "", "Give the device a name, e.g. iPhone.")
		return
	}
	_, pw, err := h.Auth.CreateAppPassword(r.Context(), user.ID, label)
	if err != nil {
		slog.Error("create app password", "err", err)
		h.renderProfile(w, r, "devices", "", "Couldn't create the app password.")
		return
	}
	h.renderProfileWith(w, r, "devices", "App password for "+label+" created. Copy it now: it won't be shown again.", "", pw)
}

// ProfileAppPasswordDelete handles POST /profile/app-passwords/{id}/delete.
func (h *Handlers) ProfileAppPasswordDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	err := h.Auth.DeleteAppPassword(r.Context(), user.ID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, auth.ErrNotFound):
		h.renderProfile(w, r, "devices", "", "That app password no longer exists.")
	case err != nil:
		slog.Error("delete app password", "err", err)
		h.renderProfile(w, r, "devices", "", "Couldn't revoke the app password.")
	default:
		h.renderProfile(w, r, "devices", "App password revoked. That device can no longer sync.", "")
	}
}

type apiAppPassword struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Password   string     `json:"password,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toAPIAppPassword(ap *auth.AppPassword) apiAppPassword {
	out := apiAppPassword{ID: ap.ID, Label: ap.Label, CreatedAt: ap.CreatedAt}
	if ap.LastUsedAt.Valid {
		t := ap.LastUsedAt.Time
		out.LastUsedAt = &t
	}
	return out
}

// APIAppPasswordList handles GET /api/v1/me/app-passwords.
func (h *Handlers) APIAppPasswordList(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	aps, err := h.Auth.ListAppPasswords(r.Context(), user.ID)
	if err != nil {
		apiInternal(w, "list app passwords", err)
		return
	}
	out := make([]apiAppPassword, 0, len(aps))
	for i := range aps {
		out = append(out, toAPIAppPassword(&aps[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_passwords": out})
}

// APIAppPasswordCreate handles POST /api/v1/me/app-passwords with
// {"label": "iPhone"}. The response is the only time the password appears.
func (h *Handlers) APIAppPasswordCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Label) == "" {
		apiInvalid(w, "label is required.")
		return
	}
	ap, pw, err := h.Auth.CreateAppPassword(r.Context(), user.ID, in.Label)
	if err != nil {
		apiInternal(w, "create app password", err)
		return
	}
	out := toAPIAppPassword(ap)
	out.Password = pw
	writeJSON(w, http.StatusCreated, out)
}

// APIAppPasswordDelete handles DELETE /api/v1/me/app-passwords/{id}.
func (h *Handlers) APIAppPasswordDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	err := h.Auth.DeleteAppPassword(r.Context(), user.ID, chi.URLParam(r, "id"))
	if errors.Is(err, auth.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "App password not found.")
		return
	}
	if err != nil {
		apiInternal(w, "delete app password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
