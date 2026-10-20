package handlers

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	appmw "github.com/andyjessop/todostuff/internal/middleware"
	"github.com/andyjessop/todostuff/internal/models"
)

// profileViewData drives the /profile page. Flash/Error/Section are scratch
// slots for the most recent submit's outcome banner.
type profileViewData struct {
	Title           string
	Theme           string
	ActiveView      string
	ActiveProjectID string
	User            *models.User
	Projects        []models.Project
	Flash           string // green banner
	Error           string // red banner
	Section         string // "profile" | "password"

	AppPasswords   []auth.AppPassword
	NewAppPassword string // plaintext of the one just created, shown once
	DAVServer      string // host:port to type into a CalDAV account
	DAVPort        string // its port, for the device's Advanced Settings
}

// ProfilePage renders /profile (GET).
func (h *Handlers) ProfilePage(w http.ResponseWriter, r *http.Request) {
	h.renderProfile(w, r, "", "", "")
}

// ProfileUpdate handles PUT/POST /profile — the user's name. Email stays
// read-only here; changes belong in admin tooling.
func (h *Handlers) ProfileUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		h.renderProfile(w, r, "profile", "", "Name can't be empty.")
		return
	}

	err := h.Auth.UpdateProfile(r.Context(), user.ID, auth.UpdateProfileParams{Name: name})
	if err != nil {
		slog.Error("update profile", "err", err)
		h.renderProfile(w, r, "profile", "", "Couldn't save your profile. Try again.")
		return
	}
	h.renderProfile(w, r, "profile", "Profile updated.", "")
}

// ProfilePassword handles POST /profile/password.
func (h *Handlers) ProfilePassword(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("confirm_password")

	switch {
	case current == "":
		h.renderProfile(w, r, "password", "", "Enter your current password.")
		return
	case len(next) < minPasswordLength:
		h.renderProfile(w, r, "password", "", "New password must be at least 8 characters.")
		return
	case next != confirm:
		h.renderProfile(w, r, "password", "", "New password and confirmation don't match.")
		return
	}

	err := h.Auth.UpdatePassword(r.Context(), user.ID, current, next)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.renderProfile(w, r, "password", "", "Current password is incorrect.")
		return
	}
	if err != nil {
		slog.Error("update password", "err", err)
		h.renderProfile(w, r, "password", "", "Couldn't change your password. Try again.")
		return
	}
	h.renderProfile(w, r, "password", "Password updated.", "")
}

// renderProfile is the single render path for /profile. Everyone funnels here
// with optional flash/error/section.
func (h *Handlers) renderProfile(w http.ResponseWriter, r *http.Request, section, flash, errMsg string) {
	h.renderProfileWith(w, r, section, flash, errMsg, "")
}

// renderProfileWith is renderProfile plus a freshly created app password to
// reveal (once) in the Connected devices card.
func (h *Handlers) renderProfileWith(w http.ResponseWriter, r *http.Request, section, flash, errMsg, newAppPassword string) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// Re-fetch so post-update values appear immediately (the request context
	// still holds the pre-update copy from the auth middleware).
	if fresh, err := h.Auth.FindUserByID(r.Context(), user.ID); err == nil {
		user = fresh
	}

	data := profileViewData{
		Title:          "Profile",
		Theme:          appmw.ThemeFromContext(r.Context()),
		ActiveView:     "profile",
		User:           user,
		Projects:       h.loadSidebarProjects(r, user.ID),
		Flash:          flash,
		Error:          errMsg,
		Section:        section,
		NewAppPassword: newAppPassword,
		DAVServer:      r.Host,
		DAVPort:        davPort(r),
	}
	if aps, err := h.Auth.ListAppPasswords(r.Context(), user.ID); err == nil {
		data.AppPasswords = aps
	} else {
		slog.Error("list app passwords", "err", err)
	}

	if err := h.Render.Render(w, http.StatusOK, "profile", "app", data); err != nil {
		slog.Error("render profile", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// davPort is the port a device should use: the one in the Host header, or
// the scheme default when the page was reached without one.
func davPort(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil {
		return port
	}
	if r.TLS != nil {
		return "443"
	}
	return "80"
}
