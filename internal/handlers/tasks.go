package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

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
		ProjectID:   r.PostFormValue("project_id"),
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

	if err := h.Render.Render(w, http.StatusOK, "today", "task-row", task); err != nil {
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
	if err := h.Render.Render(w, http.StatusOK, "today", "task-detail", task); err != nil {
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
	// is_important uses a hidden "*_present" marker so an unchecked checkbox
	// (which the browser omits entirely) still updates the field to false.
	if r.PostForm.Has("is_important_present") {
		v := parseBool(r.PostFormValue("is_important"))
		patch.IsImportant = &v
	} else if r.PostForm.Has("is_important") {
		v := parseBool(r.PostFormValue("is_important"))
		patch.IsImportant = &v
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

	// OOB swap: replaces the matching list row in place while leaving the
	// detail slide-over (the source of the PUT) untouched.
	if err := h.Render.Render(w, http.StatusOK, "today", "task-row-oob", task); err != nil {
		slog.Error("render task-row-oob", "err", err)
	}
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
