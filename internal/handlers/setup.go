package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
)

type setupViewData struct {
	Title    string
	Subtitle string
	Email    string
	Name     string
	Error    string
}

const minPasswordLength = 8

// SetupPage renders the first-run admin form. If a user already exists, the
// onboarding window is closed: authenticated visitors go to /today, the rest
// to /login.
func (h *Handlers) SetupPage(w http.ResponseWriter, r *http.Request) {
	if h.setupClosed(w, r) {
		return
	}
	data := setupViewData{
		Title:    "Create Admin",
		Subtitle: "Set up the first user for this TodoStuff instance.",
	}
	if err := h.Render.Render(w, http.StatusOK, "setup", "auth", data); err != nil {
		slog.Error("render setup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// SetupSubmit creates the first admin user, opens a session, and redirects to
// /today. Re-rendering the form with an inline error keeps the user's input.
func (h *Handlers) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	if h.setupClosed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	name := strings.TrimSpace(r.PostFormValue("name"))

	if msg := validateSetup(email, password, name); msg != "" {
		h.renderSetupError(w, email, name, msg, http.StatusBadRequest)
		return
	}

	user, err := h.Auth.CreateUser(r.Context(), auth.CreateUserParams{
		Email:    email,
		Password: password,
		Name:     name,
		IsAdmin:  true,
	})
	if err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			h.renderSetupError(w, email, name, "That email is already in use.", http.StatusBadRequest)
			return
		}
		slog.Error("create admin", "err", err)
		h.renderSetupError(w, email, name, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}

	sess, err := h.Auth.CreateSession(r.Context(), user.ID)
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Auth.SetSessionCookie(w, sess)
	http.Redirect(w, r, "/today", http.StatusFound)
}

// setupClosed sends a redirect (and returns true) once any user exists.
func (h *Handlers) setupClosed(w http.ResponseWriter, r *http.Request) bool {
	count, err := h.Auth.CountUsers(r.Context())
	if err != nil {
		slog.Error("count users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return true
	}
	if count == 0 {
		return false
	}
	if _, user, _ := h.Auth.LoadSessionFromRequest(r); user != nil {
		http.Redirect(w, r, "/today", http.StatusFound)
	} else {
		http.Redirect(w, r, "/login", http.StatusFound)
	}
	return true
}

func (h *Handlers) renderSetupError(w http.ResponseWriter, email, name, msg string, status int) {
	data := setupViewData{
		Title:    "Create Admin",
		Subtitle: "Set up the first user for this TodoStuff instance.",
		Email:    email,
		Name:     name,
		Error:    msg,
	}
	if err := h.Render.Render(w, status, "setup", "auth", data); err != nil {
		slog.Error("render setup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func validateSetup(email, password, name string) string {
	switch {
	case email == "" || !strings.Contains(email, "@"):
		return "Please enter a valid email address."
	case name == "":
		return "Please enter your name."
	case len(password) < minPasswordLength:
		return "Password must be at least 8 characters."
	}
	return ""
}
