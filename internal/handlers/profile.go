package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/notifications"
)

// profileViewData drives the /profile page. Flash/Error/Section are scratch
// slots for the most recent submit's outcome banner.
//
// FormPushoverKey carries a key the user just typed but hasn't saved yet
// (e.g. clicked "Send test notification" before "Save changes"). Coupled with
// PushoverDisplayMode it decides whether the template renders the masked
// at-rest UI or an editable input pre-filled with their draft.
type profileViewData struct {
	Title               string
	ActiveView          string
	ActiveProjectID     string
	User                *models.User
	Projects            []models.Project
	PushoverServer      bool   // is PUSHOVER_APP_TOKEN configured on the server?
	Flash               string // green banner
	Error               string // red banner
	Section             string // "profile" | "password" | "pushover"
	FormPushoverKey     string // last value the form posted, may be unsaved
	PushoverDisplayMode string // "masked" | "input"
}

// ProfilePage renders /profile (GET).
func (h *Handlers) ProfilePage(w http.ResponseWriter, r *http.Request) {
	h.renderProfile(w, r, "", "", "", "")
}

// ProfileUpdate handles PUT/POST /profile — name + Pushover key. The key's
// presence is the enable signal: non-empty key ⇒ enabled, empty ⇒ disabled.
// Email stays read-only here; changes belong in admin tooling.
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
		h.renderProfile(w, r, "profile", "", "Name can't be empty.", "")
		return
	}

	key := strings.TrimSpace(r.PostFormValue("pushover_user_key"))
	err := h.Auth.UpdateProfile(r.Context(), user.ID, auth.UpdateProfileParams{
		Name:            name,
		PushoverUserKey: key,
		PushoverEnabled: key != "",
	})
	if err != nil {
		slog.Error("update profile", "err", err)
		h.renderProfile(w, r, "profile", "", "Couldn't save your profile. Try again.", "")
		return
	}
	h.renderProfile(w, r, "profile", "Profile updated.", "", "")
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
		h.renderProfile(w, r, "password", "", "Enter your current password.", "")
		return
	case len(next) < minPasswordLength:
		h.renderProfile(w, r, "password", "", "New password must be at least 8 characters.", "")
		return
	case next != confirm:
		h.renderProfile(w, r, "password", "", "New password and confirmation don't match.", "")
		return
	}

	err := h.Auth.UpdatePassword(r.Context(), user.ID, current, next)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.renderProfile(w, r, "password", "", "Current password is incorrect.", "")
		return
	}
	if err != nil {
		slog.Error("update password", "err", err)
		h.renderProfile(w, r, "password", "", "Couldn't change your password. Try again.", "")
		return
	}
	h.renderProfile(w, r, "password", "Password updated.", "", "")
}

// ProfilePushoverTest sends a one-shot test message. Honors a key in the form
// (paste-then-test before saving) and threads that draft value back to the
// renderer so the input doesn't appear to "lose" what the user typed.
func (h *Handlers) ProfilePushoverTest(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if h.Pushover == nil || !h.Pushover.Enabled() {
		h.renderProfile(w, r, "pushover", "",
			"Pushover isn't configured on this server. Set PUSHOVER_APP_TOKEN to enable it.", "")
		return
	}

	_ = r.ParseForm()
	formKey := strings.TrimSpace(r.PostFormValue("pushover_user_key"))
	key := formKey
	if key == "" {
		key = user.PushoverUserKey.String
	}
	if key == "" {
		h.renderProfile(w, r, "pushover", "", "Enter your Pushover user key first.", formKey)
		return
	}

	err := h.Pushover.Send(r.Context(), notifications.SendParams{
		UserKey: key,
		Title:   "TodoStuff test",
		Message: "Pushover is wired up correctly. You'll get reminders here.",
	})
	switch {
	case errors.Is(err, notifications.ErrInvalidUserKey):
		h.renderProfile(w, r, "pushover", "", "Pushover rejected that user key. Double-check it on your Pushover dashboard.", formKey)
	case err != nil:
		slog.Error("pushover test", "err", err)
		h.renderProfile(w, r, "pushover", "", "Couldn't reach Pushover. Try again in a moment.", formKey)
	default:
		h.renderProfile(w, r, "pushover", "Test notification sent.", "", formKey)
	}
}

// renderProfile is the single render path for /profile. Everyone funnels here
// with optional flash/error/section + a draft Pushover key. The display mode
// (masked vs editable input) is computed server-side so the template stays
// declarative.
func (h *Handlers) renderProfile(w http.ResponseWriter, r *http.Request, section, flash, errMsg, formPushoverKey string) {
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

	// Masked at-rest UI when the user has a saved key AND no draft (or the
	// draft equals the saved key — e.g. they hit Test without editing).
	// Otherwise show an editable input pre-filled with whatever they typed.
	mode := "input"
	if user.PushoverUserKey.Valid && user.PushoverUserKey.String != "" &&
		(formPushoverKey == "" || formPushoverKey == user.PushoverUserKey.String) {
		mode = "masked"
	}

	data := profileViewData{
		Title:               "Profile",
		ActiveView:          "profile",
		User:                user,
		Projects:            h.loadSidebarProjects(r, user.ID),
		PushoverServer:      h.Pushover != nil && h.Pushover.Enabled(),
		Flash:               flash,
		Error:               errMsg,
		Section:             section,
		FormPushoverKey:     formPushoverKey,
		PushoverDisplayMode: mode,
	}

	if err := h.Render.Render(w, http.StatusOK, "profile", "app", data); err != nil {
		slog.Error("render profile", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
