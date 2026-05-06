package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// taskDetailData wraps a task plus the user's projects for the detail-panel
// project selector.
type taskDetailData struct {
	Task     models.Task
	Projects []models.Project
}

// taskRowData is the wrapper shape that the task-row family of templates
// expects. HideProject suppresses the per-row project tag — used on the
// project page where every row shares the same project.
type taskRowData struct {
	Task        models.Task
	HideProject bool
}

// hideProjectForRequest infers whether a row rendered as a side-effect of
// this request should suppress its project tag. We use the HX-Current-Url
// header (set by HTMX on every request) to detect when the user is on a
// /projects/{id} page.
func hideProjectForRequest(r *http.Request) bool {
	u := r.Header.Get("Hx-Current-Url")
	return strings.Contains(u, "/projects/")
}

// taskBelongsToCurrentView reports whether the given task should still be
// visible in the list rendered at hxCurrentURL. Used by the detail-panel
// update flow to decide between an in-place row replacement (still belongs)
// and an OOB delete swap (no longer belongs — slide the row out).
//
// Mirrors the SQL filters in services.viewQuery, kept narrow on purpose so
// behaviour stays in one place. Returns true for unknown / non-list URLs so
// the caller defaults to a harmless replace.
func taskBelongsToCurrentView(task *models.Task, hxCurrentURL string, now time.Time) bool {
	if hxCurrentURL == "" {
		return true
	}
	u, err := url.Parse(hxCurrentURL)
	if err != nil {
		return true
	}
	p := u.Path
	today := now.Format("2006-01-02")
	dueKey := ""
	if task.DueDate.Valid {
		dueKey = task.DueDate.Time.Format("2006-01-02")
	}

	if strings.HasPrefix(p, "/projects/") {
		pid := strings.TrimPrefix(p, "/projects/")
		return !task.CompletedAt.Valid &&
			task.ProjectID.Valid && task.ProjectID.String == pid
	}
	switch p {
	case "/today":
		return !task.CompletedAt.Valid && (!task.DueDate.Valid || dueKey <= today)
	case "/inbox":
		return !task.CompletedAt.Valid && !task.ProjectID.Valid && !task.DueDate.Valid
	case "/upcoming":
		return !task.CompletedAt.Valid && task.DueDate.Valid && dueKey > today
	case "/anytime":
		return !task.CompletedAt.Valid
	case "/logbook":
		return task.CompletedAt.Valid
	}
	return true
}

// TaskCreate handles POST /tasks. Returns the rendered task row partial so
// HTMX can prepend it into the list. A non-HTMX form post still works — the
// caller just sees the row HTML in the response body.
func (h *Handlers) TaskCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	p := services.CreateTaskParams{
		Title:       r.PostFormValue("title"),
		Notes:       r.PostFormValue("notes"),
		ProjectID:   strings.TrimSpace(r.PostFormValue("project_id")),
		IsImportant: parseBool(r.PostFormValue("is_important")),
		DueTime:     strings.TrimSpace(r.PostFormValue("due_time")),
	}
	if d := strings.TrimSpace(r.PostFormValue("due_date")); d != "" {
		if dt, err := time.Parse("2006-01-02", d); err == nil {
			p.DueDate = &dt
		}
	}

	task, err := h.Tasks.Create(r.Context(), user.ID, p)
	if err != nil {
		slog.Error("create task", "err", err)
		http.Error(w, "could not create task", http.StatusBadRequest)
		return
	}

	row := taskRowData{Task: *task, HideProject: hideProjectForRequest(r)}
	if err := h.Render.Render(w, http.StatusOK, "today", "task-row", row); err != nil {
		slog.Error("render task-row", "err", err)
	}
}

// TaskDetail handles GET /tasks/{id}. Returns the detail-panel partial
// suitable for swapping into #detail-panel-body.
func (h *Handlers) TaskDetail(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	task, err := h.Tasks.Get(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projects, err := h.Projects.List(r.Context(), user.ID)
	if err != nil {
		slog.Error("list projects for task detail", "err", err)
	}
	data := taskDetailData{Task: *task, Projects: projects}
	if err := h.Render.Render(w, http.StatusOK, "today", "task-detail", data); err != nil {
		slog.Error("render task-detail", "err", err)
	}
}

// TaskUpdate handles PUT /tasks/{id}. Only fields actually present in the
// form are touched; missing fields are left alone.
func (h *Handlers) TaskUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var patch services.TaskPatch
	if r.PostForm.Has("title") {
		title := r.PostFormValue("title")
		patch.Title = &title
	}
	if r.PostForm.Has("notes") {
		notes := r.PostFormValue("notes")
		patch.Notes = &sql.NullString{String: notes, Valid: notes != ""}
	}
	if r.PostForm.Has("project_id") {
		pid := strings.TrimSpace(r.PostFormValue("project_id"))
		patch.ProjectID = &sql.NullString{String: pid, Valid: pid != ""}
	}
	// is_important uses a hidden "*_present" marker so an unchecked checkbox
	// (which the browser omits entirely) still updates the field to false.
	if r.PostForm.Has("is_important_present") {
		v := parseBool(r.PostFormValue("is_important"))
		patch.IsImportant = &v
	} else if r.PostForm.Has("is_important") {
		v := parseBool(r.PostFormValue("is_important"))
		patch.IsImportant = &v
	}
	// Date/time inputs use the same _present marker pattern so an empty
	// posted value reliably means "clear it" rather than "field absent".
	dateIntent := r.PostForm.Has("due_date_present") || r.PostForm.Has("due_date")
	timeIntent := r.PostForm.Has("due_time_present") || r.PostForm.Has("due_time")
	if dateIntent {
		raw := strings.TrimSpace(r.PostFormValue("due_date"))
		if raw == "" {
			patch.DueDate = &sql.NullTime{}
			// Clearing the date implicitly clears the time. Force time clear
			// regardless of what the (now-meaningless) time input says.
			patch.DueTime = &sql.NullString{}
			timeIntent = false
		} else if dt, err := time.Parse("2006-01-02", raw); err == nil {
			patch.DueDate = &sql.NullTime{Time: dt, Valid: true}
		}
	}
	if timeIntent {
		raw := strings.TrimSpace(r.PostFormValue("due_time"))
		patch.DueTime = &sql.NullString{String: raw, Valid: raw != ""}
	}

	task, err := h.Tasks.Update(r.Context(), user.ID, id, patch)
	if err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("update task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// OOB swap. If the task still belongs to the current view, replace the
	// row in place (preserves scroll position, avoids flicker, keeps things
	// like the Important star toggling instantly). If the update moved the
	// task out of the current view's filter, send an OOB delete so the row
	// slides out — e.g. setting a date on an Inbox task, or pushing a Today
	// task to a future date.
	hxURL := r.Header.Get("Hx-Current-Url")
	if !taskBelongsToCurrentView(task, hxURL, time.Now()) {
		if err := h.Render.Render(w, http.StatusOK, "today", "task-row-oob-delete", *task); err != nil {
			slog.Error("render task-row-oob-delete", "err", err)
		}
		return
	}
	row := taskRowData{Task: *task, HideProject: hideProjectForRequest(r)}
	if err := h.Render.Render(w, http.StatusOK, "today", "task-row-oob", row); err != nil {
		slog.Error("render task-row-oob", "err", err)
	}
}

// TaskComplete handles POST /tasks/{id}/complete. Returns an empty 200 so
// the calling list row is swapped out of the DOM. The detail panel (if
// open against this task) is closed by the row form's onclick handler.
func (h *Handlers) TaskComplete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if _, err := h.Tasks.Complete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("complete task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// TaskUncomplete handles POST /tasks/{id}/uncomplete. Mirrors TaskComplete:
// the row is swapped out of the Logbook list; the task is now back in the
// active smart lists.
func (h *Handlers) TaskUncomplete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if _, err := h.Tasks.Uncomplete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("uncomplete task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// TaskDelete handles DELETE /tasks/{id}. Returns an empty 200 — HTMX will
// swap the row out of the DOM via hx-target.
func (h *Handlers) TaskDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.Tasks.Delete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("delete task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}
