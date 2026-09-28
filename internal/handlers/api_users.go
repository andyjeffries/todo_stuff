package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/notifications"
	"github.com/go-chi/chi/v5"
)

// --------------------------------------------------------------------- Me ---

// APIMe handles GET /api/v1/me.
func (h *Handlers) APIMe(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, toAPIMe(user))
}

// APIMeUpdate handles PATCH /api/v1/me: name and/or Pushover key. As in the
// web profile, a non-empty key enables Pushover and null/"" disables it.
func (h *Handlers) APIMeUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	f, ok := decodeFields(w, r, "name", "pushover_user_key")
	if !ok {
		return
	}
	p := auth.UpdateProfileParams{Name: user.Name, PushoverUserKey: user.PushoverUserKey.String}
	if f.has("name") {
		if f.isNull("name") {
			apiInvalid(w, "name cannot be null.")
			return
		}
		if !f.get(w, "name", &p.Name) {
			return
		}
		if strings.TrimSpace(p.Name) == "" {
			apiInvalid(w, "name cannot be empty.")
			return
		}
	}
	if f.has("pushover_user_key") {
		var key *string
		if !f.get(w, "pushover_user_key", &key) {
			return
		}
		p.PushoverUserKey = ""
		if key != nil {
			p.PushoverUserKey = strings.TrimSpace(*key)
		}
	}
	p.PushoverEnabled = p.PushoverUserKey != ""

	if err := h.Auth.UpdateProfile(r.Context(), user.ID, p); err != nil {
		apiInternal(w, "update profile", err)
		return
	}
	h.writeMe(w, r, user.ID)
}

// APIMePassword handles POST /api/v1/me/password. Other sessions (including
// other devices' tokens) stay valid, matching the web profile page.
func (h *Handlers) APIMePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.CurrentPassword == "" {
		apiInvalid(w, "current_password is required.")
		return
	}
	if len(in.NewPassword) < minPasswordLength {
		apiInvalid(w, "new_password must be at least 8 characters.")
		return
	}
	err := h.Auth.UpdatePassword(r.Context(), user.ID, in.CurrentPassword, in.NewPassword)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		apiError(w, http.StatusUnprocessableEntity, "incorrect_password", "Current password is incorrect.")
		return
	}
	if err != nil {
		apiInternal(w, "update password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APIMePushoverTest handles POST /api/v1/me/pushover/test. Uses the key in
// the body if given (test-before-save), else the saved key.
func (h *Handlers) APIMePushoverTest(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		PushoverUserKey string `json:"pushover_user_key"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if h.Pushover == nil || !h.Pushover.Enabled() {
		apiError(w, http.StatusServiceUnavailable, "pushover_unavailable",
			"Pushover isn't configured on this server (PUSHOVER_APP_TOKEN is unset).")
		return
	}
	key := strings.TrimSpace(in.PushoverUserKey)
	if key == "" {
		key = user.PushoverUserKey.String
	}
	if key == "" {
		apiInvalid(w, "No Pushover user key saved or supplied.")
		return
	}
	err := h.Pushover.Send(r.Context(), notifications.SendParams{
		UserKey: key,
		Title:   "TodoStuff test",
		Message: "Pushover is wired up correctly. You'll get reminders here.",
	})
	switch {
	case errors.Is(err, notifications.ErrInvalidUserKey):
		apiError(w, http.StatusUnprocessableEntity, "invalid_pushover_key", "Pushover rejected that user key.")
	case err != nil:
		apiError(w, http.StatusBadGateway, "pushover_error", "Couldn't reach Pushover. Try again in a moment.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handlers) writeMe(w http.ResponseWriter, r *http.Request, id string) {
	u, err := h.Auth.FindUserByID(r.Context(), id)
	if err != nil {
		apiInternal(w, "reload user", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIMe(u))
}

// ------------------------------------------------------------------ Admin ---

// APIAdminUserList handles GET /api/v1/admin/users.
func (h *Handlers) APIAdminUserList(w http.ResponseWriter, r *http.Request) {
	users, err := h.Auth.ListUsers(r.Context())
	if err != nil {
		apiInternal(w, "list users", err)
		return
	}
	out := make([]apiUser, 0, len(users))
	for _, u := range users {
		out = append(out, toAPIUser(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// APIAdminUserGet handles GET /api/v1/admin/users/{id}.
func (h *Handlers) APIAdminUserGet(w http.ResponseWriter, r *http.Request) {
	u, err := h.Auth.FindUserByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		apiUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

// APIAdminUserCreate handles POST /api/v1/admin/users.
func (h *Handlers) APIAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Email, in.Name = strings.TrimSpace(in.Email), strings.TrimSpace(in.Name)
	if msg := validateNewUser(in.Email, in.Password, in.Name); msg != "" {
		apiInvalid(w, msg)
		return
	}
	u, err := h.Auth.CreateUser(r.Context(), auth.CreateUserParams{
		Email: in.Email, Password: in.Password, Name: in.Name, IsAdmin: in.IsAdmin,
	})
	if err != nil {
		apiUserErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPIUser(u))
}

// APIAdminUserUpdate handles PATCH /api/v1/admin/users/{id}: email, name,
// and/or is_admin. Same rails as the web: you can't demote yourself.
func (h *Handlers) APIAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	f, ok := decodeFields(w, r, "email", "name", "is_admin")
	if !ok {
		return
	}
	target, err := h.Auth.FindUserByID(r.Context(), id)
	if err != nil {
		apiUserErr(w, err)
		return
	}
	p := auth.UpdateUserParams{Email: target.Email, Name: target.Name, IsAdmin: target.IsAdmin}
	for key, dst := range map[string]any{"email": &p.Email, "name": &p.Name, "is_admin": &p.IsAdmin} {
		if !f.has(key) {
			continue
		}
		if f.isNull(key) {
			apiInvalid(w, key+" cannot be null.")
			return
		}
		if !f.get(w, key, dst) {
			return
		}
	}
	if id == current.ID && !p.IsAdmin {
		apiError(w, http.StatusUnprocessableEntity, "cannot_demote_self",
			"You can't remove your own admin access. Ask another admin to do it.")
		return
	}
	if err := h.Auth.UpdateUser(r.Context(), id, p); err != nil {
		apiUserErr(w, err)
		return
	}
	u, err := h.Auth.FindUserByID(r.Context(), id)
	if err != nil {
		apiInternal(w, "reload user", err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

// APIAdminUserDelete handles DELETE /api/v1/admin/users/{id}. Deletes the
// user and all their data. Refuses self-deletion and the last admin.
func (h *Handlers) APIAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if id == current.ID {
		apiError(w, http.StatusUnprocessableEntity, "cannot_delete_self", "You can't delete your own account.")
		return
	}
	target, err := h.Auth.FindUserByID(r.Context(), id)
	if err != nil {
		apiUserErr(w, err)
		return
	}
	if target.IsAdmin {
		count, err := h.Auth.CountAdmins(r.Context())
		if err != nil {
			apiInternal(w, "count admins", err)
			return
		}
		if count <= 1 {
			apiError(w, http.StatusUnprocessableEntity, "last_admin", "Can't delete the last admin.")
			return
		}
	}
	if err := h.Auth.DeleteUser(r.Context(), id); err != nil {
		apiUserErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APIAdminUserResetPassword handles POST /api/v1/admin/users/{id}/reset-password.
// Signs the user out everywhere (all their sessions/tokens are revoked).
func (h *Handlers) APIAdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.NewPassword) < minPasswordLength {
		apiInvalid(w, "new_password must be at least 8 characters.")
		return
	}
	if err := h.Auth.ResetUserPassword(r.Context(), chi.URLParam(r, "id"), in.NewPassword); err != nil {
		apiUserErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func apiUserErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "User not found.")
	case errors.Is(err, auth.ErrEmailTaken):
		apiError(w, http.StatusConflict, "email_taken", "That email is already in use.")
	case strings.HasPrefix(err.Error(), "auth: "):
		// Service-level validation (e.g. malformed email) — message is safe to show.
		apiInvalid(w, strings.TrimPrefix(err.Error(), "auth: ")+".")
	default:
		apiInternal(w, "user", err)
	}
}
