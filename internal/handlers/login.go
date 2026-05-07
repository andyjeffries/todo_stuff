package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	appmw "github.com/andyjessop/todostuff/internal/middleware"
)

type loginViewData struct {
	Title    string
	Theme    string
	Subtitle string
	Email    string
	Remember bool
	Error    string
}

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	if _, user, _ := h.Auth.LoadSessionFromRequest(r); user != nil {
		http.Redirect(w, r, "/today", http.StatusFound)
		return
	}
	data := loginViewData{Title: "Sign in", Subtitle: "Welcome back.", Theme: appmw.ThemeFromContext(r.Context())}
	if err := h.Render.Render(w, http.StatusOK, "login", "auth", data); err != nil {
		slog.Error("render login", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	remember := r.PostFormValue("remember") != ""

	user, err := h.Auth.Authenticate(r.Context(), email, password)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			slog.Error("authenticate", "err", err)
		}
		data := loginViewData{Title: "Sign in", Subtitle: "Welcome back.", Email: email, Remember: remember,
			Error: "Email or password is incorrect.", Theme: appmw.ThemeFromContext(r.Context())}
		_ = h.Render.Render(w, http.StatusUnauthorized, "login", "auth", data)
		return
	}

	sess, err := h.Auth.CreateSession(r.Context(), user.ID, remember)
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Auth.SetSessionCookie(w, sess)
	http.Redirect(w, r, "/today", http.StatusFound)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if err := h.Auth.DeleteSession(r.Context(), cookie.Value); err != nil {
			slog.Error("delete session", "err", err)
		}
	}
	h.Auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}
