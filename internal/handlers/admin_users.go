package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	appmw "github.com/andyjessop/todostuff/internal/middleware"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/go-chi/chi/v5"
)

// adminUsersData drives /admin/users.
type adminUsersData struct {
	appViewData
	Users []*models.User
	Form  adminUserFormState
}

// adminUserEditData drives /admin/users/{id}.
type adminUserEditData struct {
	appViewData
	Target *models.User
	IsSelf bool
	// LastAdmin is true when this is the only remaining admin; used by the
	// template to grey out the demote/delete affordances. Saves the user a
	// round-trip into the friendly error.
	LastAdmin     bool
	Form          adminUserFormState
	PasswordFlash string
	PasswordError string
}

// adminUserFormState carries the most recent submit's draft values + status
// banner. Re-render-on-error keeps the user's typing.
type adminUserFormState struct {
	Email   string
	Name    string
	IsAdmin bool
	Flash   string // green banner
	Error   string // red banner
}

// AdminUsersPage renders the user list + inline create form.
func (h *Handlers) AdminUsersPage(w http.ResponseWriter, r *http.Request) {
	h.renderAdminUsers(w, r, adminUserFormState{})
}

// AdminUserCreate handles POST /admin/users.
func (h *Handlers) AdminUserCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	name := strings.TrimSpace(r.PostFormValue("name"))
	isAdmin := parseBoolField(r.PostFormValue("is_admin"))

	form := adminUserFormState{Email: email, Name: name, IsAdmin: isAdmin}
	if msg := validateNewUser(email, password, name); msg != "" {
		form.Error = msg
		h.renderAdminUsersStatus(w, r, form, http.StatusBadRequest)
		return
	}

	_, err := h.Auth.CreateUser(r.Context(), auth.CreateUserParams{
		Email:    email,
		Password: password,
		Name:     name,
		IsAdmin:  isAdmin,
	})
	if errors.Is(err, auth.ErrEmailTaken) {
		form.Error = "That email is already in use."
		h.renderAdminUsersStatus(w, r, form, http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("admin create user", "err", err)
		form.Error = "Couldn't create the user. Try again."
		h.renderAdminUsersStatus(w, r, form, http.StatusInternalServerError)
		return
	}

	h.renderAdminUsers(w, r, adminUserFormState{Flash: "User created."})
}

// AdminUserEditPage renders the per-user edit page.
func (h *Handlers) AdminUserEditPage(w http.ResponseWriter, r *http.Request) {
	h.renderAdminUserEdit(w, r, adminUserFormState{}, "", "")
}

// AdminUserUpdate handles PUT /admin/users/{id}: name + email + admin flag.
func (h *Handlers) AdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.PostFormValue("email"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	isAdmin := parseBoolField(r.PostFormValue("is_admin"))

	// Self-protection: an admin can't strip their own admin flag — that's
	// how a single-admin instance ends up with no admins at all. Even with
	// other admins around it's a footgun, so we block it unconditionally.
	if id == current.ID && current.IsAdmin && !isAdmin {
		isAdmin = true
		h.renderAdminUserEdit(w, r,
			adminUserFormState{Email: email, Name: name, IsAdmin: isAdmin,
				Error: "You can't remove your own admin access. Ask another admin to do it."},
			"", "")
		return
	}

	err := h.Auth.UpdateUser(r.Context(), id, auth.UpdateUserParams{
		Email:   email,
		Name:    name,
		IsAdmin: isAdmin,
	})
	form := adminUserFormState{Email: email, Name: name, IsAdmin: isAdmin}
	switch {
	case errors.Is(err, auth.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, auth.ErrEmailTaken):
		form.Error = "That email is already in use."
		h.renderAdminUserEdit(w, r, form, "", "")
		return
	case err != nil:
		// Validation errors from the service surface their message verbatim.
		slog.Error("admin update user", "err", err)
		form.Error = "Couldn't save changes. " + err.Error()
		h.renderAdminUserEdit(w, r, form, "", "")
		return
	}

	form.Flash = "User updated."
	h.renderAdminUserEdit(w, r, form, "", "")
}

// AdminUserDelete handles DELETE /admin/users/{id}.
func (h *Handlers) AdminUserDelete(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if id == current.ID {
		http.Error(w, "you can't delete your own account", http.StatusBadRequest)
		return
	}

	// Refuse to remove the last admin even if the request comes from another
	// admin — but in practice the only admin can't be deleted by themselves
	// (above), and any non-admin target trivially passes this check.
	target, err := h.Auth.FindUserByID(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("admin delete: lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if target.IsAdmin {
		count, err := h.Auth.CountAdmins(r.Context())
		if err != nil {
			slog.Error("admin delete: count admins", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count <= 1 {
			http.Error(w, "can't delete the last admin", http.StatusBadRequest)
			return
		}
	}

	if err := h.Auth.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin delete user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// HTMX clients honour HX-Redirect; plain form posts get the same effect
	// via a 303-style redirect.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/users")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// AdminUserResetPassword handles POST /admin/users/{id}/reset-password.
func (h *Handlers) AdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("confirm_password")

	switch {
	case len(next) < minPasswordLength:
		h.renderAdminUserEdit(w, r, adminUserFormState{}, "",
			"New password must be at least 8 characters.")
		return
	case next != confirm:
		h.renderAdminUserEdit(w, r, adminUserFormState{}, "",
			"New password and confirmation don't match.")
		return
	}

	if err := h.Auth.ResetUserPassword(r.Context(), id, next); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin reset password", "err", err)
		h.renderAdminUserEdit(w, r, adminUserFormState{}, "",
			"Couldn't reset password. Try again.")
		return
	}

	h.renderAdminUserEdit(w, r, adminUserFormState{},
		"Password reset. The user has been signed out everywhere.", "")
}

// renderAdminUsers funnels into a 200 response. renderAdminUsersStatus is the
// non-200 sibling so we can return 4xx on validation while still showing the
// page so the user can correct and resubmit.
func (h *Handlers) renderAdminUsers(w http.ResponseWriter, r *http.Request, form adminUserFormState) {
	h.renderAdminUsersStatus(w, r, form, http.StatusOK)
}

func (h *Handlers) renderAdminUsersStatus(w http.ResponseWriter, r *http.Request, form adminUserFormState, status int) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	users, err := h.Auth.ListUsers(r.Context())
	if err != nil {
		slog.Error("list users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := adminUsersData{
		appViewData: appViewData{
			Title:      "Users",
			Theme:      appmw.ThemeFromContext(r.Context()),
			ActiveView: "admin-users",
			Heading:    "Users",
			Subheading: "Manage who can sign in to this TodoStuff instance.",
			User:       user,
			Projects:   h.loadSidebarProjects(r, user.ID),
		},
		Users: users,
		Form:  form,
	}

	if err := h.Render.Render(w, status, "admin_users", "app", data); err != nil {
		slog.Error("render admin_users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// renderAdminUserEdit renders the per-user edit page. Empty form values
// trigger a fresh load from the DB so the page always reflects truth after a
// successful save; non-empty values mean the caller is re-rendering with a
// draft after a validation error.
func (h *Handlers) renderAdminUserEdit(w http.ResponseWriter, r *http.Request, form adminUserFormState, pwFlash, pwErr string) {
	current, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	id := chi.URLParam(r, "id")

	target, err := h.Auth.FindUserByID(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("find user for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if form.Email == "" && form.Name == "" {
		form.Email = target.Email
		form.Name = target.Name
		form.IsAdmin = target.IsAdmin
	}

	adminCount, err := h.Auth.CountAdmins(r.Context())
	if err != nil {
		slog.Error("count admins", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := adminUserEditData{
		appViewData: appViewData{
			Title:      target.Name,
			Theme:      appmw.ThemeFromContext(r.Context()),
			ActiveView: "admin-users",
			Heading:    target.Name,
			Subheading: target.Email,
			User:       current,
			Projects:   h.loadSidebarProjects(r, current.ID),
		},
		Target:        target,
		IsSelf:        target.ID == current.ID,
		LastAdmin:     target.IsAdmin && adminCount <= 1,
		Form:          form,
		PasswordFlash: pwFlash,
		PasswordError: pwErr,
	}

	if err := h.Render.Render(w, http.StatusOK, "admin_user_edit", "app", data); err != nil {
		slog.Error("render admin_user_edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// validateNewUser rejects bad submissions at the handler boundary so the
// service layer can stay focused on storage. Mirrors validateSetup but adds
// the email/password/name combo for plain user creation.
func validateNewUser(email, password, name string) string {
	switch {
	case email == "" || !strings.Contains(email, "@"):
		return "Please enter a valid email address."
	case name == "":
		return "Please enter a name."
	case len(password) < minPasswordLength:
		return "Password must be at least 8 characters."
	}
	return ""
}

// parseBoolField accepts the values HTML forms typically send for a boolean:
// "1"/"0", "true"/"false", "on" (the default for an unnamed checkbox value),
// or empty. Anything unknown reads as false — matches what the existing
// profile/setup forms expect.
func parseBoolField(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}
