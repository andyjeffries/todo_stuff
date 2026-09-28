package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	appmw "github.com/andyjessop/todostuff/internal/middleware"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/go-chi/chi/v5"
)

// The JSON API lives under /api/v1 and is documented in docs/API.md. It's a
// peer of the HTML handlers: same services, same validation rules, but JSON
// in and out and Bearer-token auth so native clients can drive everything the
// web UI can.

const apiMaxBodyBytes = 1 << 20

// APIRouter builds the /api/v1 sub-router. main mounts it with r.Mount.
func (h *Handlers) APIRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(appmw.Recover(func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, http.StatusInternalServerError, "internal_error", "Internal server error.")
	}))
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, http.StatusNotFound, "not_found", "No such API endpoint.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed on this endpoint.")
	})

	// Public
	r.Get("/status", h.APIStatus)
	r.Post("/setup", h.APISetup)
	r.Post("/auth/login", h.APILogin)

	r.Group(func(ar chi.Router) {
		ar.Use(appmw.RequireAPIAuth(h.Auth))

		ar.Post("/auth/logout", h.APILogout)
		ar.Get("/sync", h.APISync)

		ar.Get("/me", h.APIMe)
		ar.Patch("/me", h.APIMeUpdate)
		ar.Post("/me/password", h.APIMePassword)
		ar.Post("/me/pushover/test", h.APIMePushoverTest)

		ar.Get("/tasks", h.APITaskList)
		ar.Post("/tasks", h.APITaskCreate)
		ar.Post("/tasks/reorder", h.APITaskReorder)
		ar.Get("/tasks/{id}", h.APITaskGet)
		ar.Patch("/tasks/{id}", h.APITaskUpdate)
		ar.Delete("/tasks/{id}", h.APITaskDelete)
		ar.Post("/tasks/{id}/complete", h.APITaskComplete)
		ar.Post("/tasks/{id}/uncomplete", h.APITaskUncomplete)

		ar.Get("/search", h.APISearch)
		ar.Post("/reminders/claim", h.APIRemindersClaim)

		ar.Get("/projects", h.APIProjectList)
		ar.Post("/projects", h.APIProjectCreate)
		ar.Post("/projects/reorder", h.APIProjectReorder)
		ar.Get("/projects/{id}", h.APIProjectGet)
		ar.Patch("/projects/{id}", h.APIProjectUpdate)
		ar.Delete("/projects/{id}", h.APIProjectDelete)
		ar.Post("/projects/{id}/move", h.APIProjectMove)

		ar.Group(func(adm chi.Router) {
			adm.Use(appmw.RequireAPIAdmin)
			adm.Get("/admin/users", h.APIAdminUserList)
			adm.Post("/admin/users", h.APIAdminUserCreate)
			adm.Get("/admin/users/{id}", h.APIAdminUserGet)
			adm.Patch("/admin/users/{id}", h.APIAdminUserUpdate)
			adm.Delete("/admin/users/{id}", h.APIAdminUserDelete)
			adm.Post("/admin/users/{id}/reset-password", h.APIAdminUserResetPassword)
		})
	})
	return r
}

// ------------------------------------------------------------ JSON plumbing ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode api response", "err", err)
	}
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	appmw.WriteJSONError(w, status, code, message)
}

func apiInternal(w http.ResponseWriter, what string, err error) {
	slog.Error("api: "+what, "err", err)
	apiError(w, http.StatusInternalServerError, "internal_error", "Internal server error.")
}

func apiInvalid(w http.ResponseWriter, message string) {
	apiError(w, http.StatusUnprocessableEntity, "validation_failed", message)
}

// readJSONBody returns the raw request body, enforcing the size cap and the
// JSON content type. An empty body reads as `{}` so endpoints with only
// optional fields can be called bare. Writes the error response and returns
// ok=false on failure.
func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, apiMaxBodyBytes))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body exceeds 1 MiB.")
		return nil, false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return []byte("{}"), true
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		apiError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send request bodies as application/json.")
		return nil, false
	}
	return body, true
}

// decodeJSON decodes the body into dst, rejecting unknown fields so client
// typos surface as errors instead of silently doing nothing.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, ok := readJSONBody(w, r)
	if !ok {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", "Invalid JSON: "+err.Error())
		return false
	}
	return true
}

// jsonFields is a decoded PATCH body. It keeps the raw value per key so
// handlers can tell "absent" (leave alone) from `null` (clear).
type jsonFields map[string]json.RawMessage

func decodeFields(w http.ResponseWriter, r *http.Request, allowed ...string) (jsonFields, bool) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return nil, false
	}
	var f jsonFields
	if err := json.Unmarshal(body, &f); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", "Invalid JSON: expected an object.")
		return nil, false
	}
	for k := range f {
		known := false
		for _, a := range allowed {
			if k == a {
				known = true
				break
			}
		}
		if !known {
			apiError(w, http.StatusBadRequest, "bad_request", "Unknown field "+k+".")
			return nil, false
		}
	}
	return f, true
}

func (f jsonFields) has(k string) bool { _, ok := f[k]; return ok }

func (f jsonFields) isNull(k string) bool {
	return f.has(k) && string(bytes.TrimSpace(f[k])) == "null"
}

// get unmarshals field k into dst. On a type mismatch it writes a 422 naming
// the field and returns false.
func (f jsonFields) get(w http.ResponseWriter, k string, dst any) bool {
	if err := json.Unmarshal(f[k], dst); err != nil {
		apiInvalid(w, "Field "+k+" has the wrong type.")
		return false
	}
	return true
}

// --------------------------------------------------------------- Wire types ---

type apiUser struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	IsAdmin         bool      `json:"is_admin"`
	PushoverEnabled bool      `json:"pushover_enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// apiMe is the signed-in user's own view of themselves: apiUser plus their
// Pushover key, which admins listing other users never see.
type apiMe struct {
	apiUser
	PushoverUserKey *string `json:"pushover_user_key"`
}

func toAPIUser(u *models.User) apiUser {
	return apiUser{
		ID:              u.ID,
		Email:           u.Email,
		Name:            u.Name,
		IsAdmin:         u.IsAdmin,
		PushoverEnabled: u.PushoverEnabled,
		CreatedAt:       u.CreatedAt.UTC(),
		UpdatedAt:       u.UpdatedAt.UTC(),
	}
}

func toAPIMe(u *models.User) apiMe {
	return apiMe{apiUser: toAPIUser(u), PushoverUserKey: nullString(u.PushoverUserKey.String, u.PushoverUserKey.Valid)}
}

type apiProject struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Icon      *string   `json:"icon"`
	Position  int64     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toAPIProject(p *models.Project) apiProject {
	return apiProject{
		ID:        p.ID,
		Name:      p.Name,
		Icon:      nullString(p.Icon.String, p.Icon.Valid),
		Position:  p.Position,
		CreatedAt: p.CreatedAt.UTC(),
		UpdatedAt: p.UpdatedAt.UTC(),
	}
}

type apiRecurrence struct {
	Frequency        string `json:"frequency"`
	Interval         int    `json:"interval"`
	RegenerationType string `json:"regeneration_type"`
}

type apiTask struct {
	ID                    string         `json:"id"`
	Title                 string         `json:"title"`
	Notes                 *string        `json:"notes"`
	NotesHTML             *string        `json:"notes_html"`
	ProjectID             *string        `json:"project_id"`
	ProjectName           *string        `json:"project_name"`
	ProjectIcon           *string        `json:"project_icon"`
	IsImportant           bool           `json:"is_important"`
	DueDate               *string        `json:"due_date"`
	DueTime               *string        `json:"due_time"`
	ReminderOffsetMinutes *int64         `json:"reminder_offset_minutes"`
	ReminderAt            *time.Time     `json:"reminder_at"`
	CompletedAt           *time.Time     `json:"completed_at"`
	Position              int64          `json:"position"`
	Recurrence            *apiRecurrence `json:"recurrence"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

func toAPITask(t *models.Task) apiTask {
	out := apiTask{
		ID:          t.ID,
		Title:       t.Title,
		Notes:       nullString(t.Notes.String, t.Notes.Valid),
		NotesHTML:   nullString(t.NotesHTML.String, t.NotesHTML.Valid),
		ProjectID:   nullString(t.ProjectID.String, t.ProjectID.Valid),
		ProjectName: nullString(t.ProjectName.String, t.ProjectName.Valid),
		ProjectIcon: nullString(t.ProjectIcon.String, t.ProjectIcon.Valid),
		IsImportant: t.IsImportant,
		Position:    t.Position,
		CreatedAt:   t.CreatedAt.UTC(),
		UpdatedAt:   t.UpdatedAt.UTC(),
	}
	if t.DueDate.Valid {
		d := t.DueDate.Time.Format("2006-01-02")
		out.DueDate = &d
	}
	if t.DueTime.Valid {
		hm := hhmm(t.DueTime.String)
		out.DueTime = &hm
	}
	if t.ReminderOffsetMinutes.Valid {
		n := t.ReminderOffsetMinutes.Int64
		out.ReminderOffsetMinutes = &n
	}
	if t.ReminderAt.Valid {
		at := t.ReminderAt.Time.UTC()
		out.ReminderAt = &at
	}
	if t.CompletedAt.Valid {
		at := t.CompletedAt.Time.UTC()
		out.CompletedAt = &at
	}
	if t.RecurrenceRuleID.Valid && t.RecurrenceFrequency.Valid {
		out.Recurrence = &apiRecurrence{
			Frequency:        t.RecurrenceFrequency.String,
			Interval:         int(t.RecurrenceInterval.Int64),
			RegenerationType: t.RecurrenceType.String,
		}
	}
	return out
}

func toAPITasks(tasks []models.Task) []apiTask {
	out := make([]apiTask, 0, len(tasks))
	for i := range tasks {
		out = append(out, toAPITask(&tasks[i]))
	}
	return out
}

func nullString(s string, valid bool) *string {
	if !valid {
		return nil
	}
	return &s
}

// hhmm trims a stored TIME value ("HH:MM" or "HH:MM:SS") to "HH:MM".
func hhmm(s string) string {
	if len(s) > 5 {
		return s[:5]
	}
	return s
}

// ------------------------------------------------------ Status, setup, auth ---

// APIStatus handles GET /api/v1/status. Public: lets a client discover
// whether the instance needs first-run setup and which timezone the server
// uses to interpret due dates/times.
func (h *Handlers) APIStatus(w http.ResponseWriter, r *http.Request) {
	count, err := h.Auth.CountUsers(r.Context())
	if err != nil {
		apiInternal(w, "count users", err)
		return
	}
	now := time.Now()
	_, offset := now.Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               "TodoStuff",
		"api_version":        1,
		"needs_setup":        count == 0,
		"pushover_available": h.Pushover != nil && h.Pushover.Enabled(),
		"timezone":           serverTimezone(),
		"utc_offset_seconds": offset,
		"server_time":        now.UTC(),
	})
}

// serverTimezone names the zone the server interprets due dates/times in.
// Go reports "Local" when TZ is unset, so fall back to the /etc/localtime
// symlink target (".../zoneinfo/Europe/London") to recover the IANA name.
func serverTimezone() string {
	if name := time.Local.String(); name != "Local" {
		return name
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(target, "zoneinfo/"); ok {
			return zone
		}
	}
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	return "Local"
}

type apiSessionResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      apiMe     `json:"user"`
}

// APISetup handles POST /api/v1/setup — the JSON twin of /setup. Only works
// while the instance has no users.
func (h *Handlers) APISetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	count, err := h.Auth.CountUsers(r.Context())
	if err != nil {
		apiInternal(w, "count users", err)
		return
	}
	if count > 0 {
		apiError(w, http.StatusConflict, "already_set_up", "This instance already has users; sign in instead.")
		return
	}
	in.Email, in.Name = strings.TrimSpace(in.Email), strings.TrimSpace(in.Name)
	if msg := validateSetup(in.Email, in.Password, in.Name); msg != "" {
		apiInvalid(w, msg)
		return
	}
	user, err := h.Auth.CreateUser(r.Context(), auth.CreateUserParams{
		Email: in.Email, Password: in.Password, Name: in.Name, IsAdmin: true,
	})
	if err != nil {
		apiInternal(w, "create admin", err)
		return
	}
	h.issueSession(w, r, user, true, http.StatusCreated)
}

// APILogin handles POST /api/v1/auth/login. The returned token is a normal
// session (same table as browser cookies), sent back as a Bearer token.
func (h *Handlers) APILogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Remember *bool  `json:"remember"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	user, err := h.Auth.Authenticate(r.Context(), in.Email, in.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		apiError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if err != nil {
		apiInternal(w, "authenticate", err)
		return
	}
	// Native clients are "remembered devices" by default: 365-day token.
	remember := in.Remember == nil || *in.Remember
	h.issueSession(w, r, user, remember, http.StatusOK)
}

func (h *Handlers) issueSession(w http.ResponseWriter, r *http.Request, user *models.User, remember bool, status int) {
	sess, err := h.Auth.CreateSession(r.Context(), user.ID, remember)
	if err != nil {
		apiInternal(w, "create session", err)
		return
	}
	writeJSON(w, status, apiSessionResponse{
		Token:     sess.ID,
		ExpiresAt: sess.ExpiresAt.UTC(),
		User:      toAPIMe(user),
	})
}

// APILogout handles POST /api/v1/auth/logout: revokes the calling token.
func (h *Handlers) APILogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := auth.SessionFromContext(r.Context()); ok {
		if err := h.Auth.DeleteSession(r.Context(), sess.ID); err != nil {
			apiInternal(w, "delete session", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// APISync handles GET /api/v1/sync. Returns an opaque token that changes
// whenever the user's tasks, projects, or profile change — a cheap poll so
// clients only refetch lists when something actually moved.
func (h *Handlers) APISync(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	tok, err := h.Tasks.SyncToken(r.Context(), user.ID)
	if err != nil {
		apiInternal(w, "sync token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sync_token": tok})
}
