package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// taskFields are the writable task attributes, shared by create and update.
var taskFields = []string{
	"title", "notes", "project_id", "is_important",
	"due_date", "due_time", "reminder_offset_minutes", "recurrence",
}

var apiViews = map[string]services.View{
	"today":    services.ViewToday,
	"inbox":    services.ViewInbox,
	"upcoming": services.ViewUpcoming,
	"anytime":  services.ViewAnytime,
	"logbook":  services.ViewLogbook,
}

// APITaskList handles GET /api/v1/tasks?view=… or ?project_id=…. The views
// match the sidebar's smart lists exactly (same SQL, same ordering); a
// project_id returns that project's open tasks like the project page.
func (h *Handlers) APITaskList(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	q := r.URL.Query()
	viewName, projectID := q.Get("view"), q.Get("project_id")
	if viewName != "" && projectID != "" {
		apiError(w, http.StatusBadRequest, "bad_request", "Pass either view or project_id, not both.")
		return
	}

	var (
		tasks []models.Task
		err   error
	)
	if projectID != "" {
		if _, err := h.Projects.Get(r.Context(), user.ID, projectID); err != nil {
			h.apiProjectErr(w, err)
			return
		}
		tasks, err = h.Tasks.ListByProject(r.Context(), user.ID, projectID)
	} else {
		if viewName == "" {
			viewName = "anytime"
		}
		view, ok := apiViews[viewName]
		if !ok {
			apiError(w, http.StatusBadRequest, "bad_request", "Unknown view "+strconv.Quote(viewName)+"; use today, inbox, upcoming, anytime, or logbook.")
			return
		}
		tasks, err = h.Tasks.ListByView(r.Context(), user.ID, view)
	}
	if err != nil {
		apiInternal(w, "list tasks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": toAPITasks(tasks)})
}

// APITaskGet handles GET /api/v1/tasks/{id}.
func (h *Handlers) APITaskGet(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	task, err := h.Tasks.Get(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.apiTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITask(task))
}

// APITaskCreate handles POST /api/v1/tasks. Accepts the same fields as
// PATCH (validated by the same code) with title required.
func (h *Handlers) APITaskCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	f, ok := decodeFields(w, r, taskFields...)
	if !ok {
		return
	}
	patch, rec, ok := h.buildTaskPatch(w, r, user.ID, f, &models.Task{})
	if !ok {
		return
	}
	if patch.Title == nil {
		apiInvalid(w, "title is required.")
		return
	}

	p := services.CreateTaskParams{Title: *patch.Title}
	if patch.Notes != nil {
		p.Notes = patch.Notes.String
	}
	if patch.ProjectID != nil {
		p.ProjectID = patch.ProjectID.String
	}
	if patch.IsImportant != nil {
		p.IsImportant = *patch.IsImportant
	}
	if patch.DueDate != nil && patch.DueDate.Valid {
		d := patch.DueDate.Time
		p.DueDate = &d
	}
	if patch.DueTime != nil {
		p.DueTime = patch.DueTime.String
	}

	task, err := h.Tasks.Create(r.Context(), user.ID, p)
	if err != nil {
		apiInternal(w, "create task", err)
		return
	}
	if patch.ReminderAt != nil {
		if _, err := h.Tasks.Update(r.Context(), user.ID, task.ID, services.TaskPatch{
			ReminderAt:            patch.ReminderAt,
			ReminderOffsetMinutes: patch.ReminderOffsetMinutes,
		}); err != nil {
			apiInternal(w, "set reminder on create", err)
			return
		}
	}
	if rec != nil {
		if err := h.Tasks.SetRecurrenceForTask(r.Context(), user.ID, task.ID, *rec); err != nil {
			apiInternal(w, "set recurrence on create", err)
			return
		}
	}
	h.writeTask(w, r, user.ID, task.ID, http.StatusCreated)
}

// APITaskUpdate handles PATCH /api/v1/tasks/{id}. Absent fields are left
// alone; `null` clears a nullable field.
func (h *Handlers) APITaskUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	f, ok := decodeFields(w, r, taskFields...)
	if !ok {
		return
	}
	existing, err := h.Tasks.Get(r.Context(), user.ID, id)
	if err != nil {
		h.apiTaskErr(w, err)
		return
	}
	patch, rec, ok := h.buildTaskPatch(w, r, user.ID, f, existing)
	if !ok {
		return
	}

	if _, err := h.Tasks.Update(r.Context(), user.ID, id, patch); err != nil {
		h.apiTaskErr(w, err)
		return
	}
	// After the field update, so the rule's template snapshots the new state
	// (same ordering as the HTML handler).
	if rec != nil {
		if err := h.Tasks.SetRecurrenceForTask(r.Context(), user.ID, id, *rec); err != nil {
			apiInternal(w, "set recurrence on update", err)
			return
		}
	}
	h.writeTask(w, r, user.ID, id, http.StatusOK)
}

// buildTaskPatch validates a create/update body against the task's current
// state and translates it into a service patch plus optional recurrence
// change. Everything is validated before anything is written. On failure it
// has already written the error response.
//
// Reminders mirror the web UI: the client sends an offset, the server derives
// reminder_at from the task's final due date + time. Changing the date or
// time of a task that has a reminder moves the reminder with it.
func (h *Handlers) buildTaskPatch(w http.ResponseWriter, r *http.Request, userID string, f jsonFields, existing *models.Task) (services.TaskPatch, *services.RecurrenceParams, bool) {
	var patch services.TaskPatch
	fail := func(msg string) (services.TaskPatch, *services.RecurrenceParams, bool) {
		apiInvalid(w, msg)
		return services.TaskPatch{}, nil, false
	}
	// abort is for failures where the error response is already written.
	abort := func() (services.TaskPatch, *services.RecurrenceParams, bool) {
		return services.TaskPatch{}, nil, false
	}

	if f.has("title") {
		var title string
		if f.isNull("title") {
			return fail("title cannot be null.")
		}
		if !f.get(w, "title", &title) {
			return abort()
		}
		if strings.TrimSpace(title) == "" {
			return fail("title cannot be empty.")
		}
		patch.Title = &title
	}

	if f.has("notes") {
		var notes *string
		if !f.get(w, "notes", &notes) {
			return abort()
		}
		v := ""
		if notes != nil {
			v = *notes
		}
		patch.Notes = &sql.NullString{String: v, Valid: strings.TrimSpace(v) != ""}
	}

	if f.has("project_id") {
		var pid *string
		if !f.get(w, "project_id", &pid) {
			return abort()
		}
		if pid == nil || strings.TrimSpace(*pid) == "" {
			patch.ProjectID = &sql.NullString{}
		} else {
			if _, err := h.Projects.Get(r.Context(), userID, *pid); err != nil {
				if errors.Is(err, services.ErrProjectNotFound) {
					return fail("project_id doesn't match any of your projects.")
				}
				apiInternal(w, "look up project", err)
				return abort()
			}
			patch.ProjectID = &sql.NullString{String: *pid, Valid: true}
		}
	}

	if f.has("is_important") {
		var v bool
		if f.isNull("is_important") {
			return fail("is_important cannot be null.")
		}
		if !f.get(w, "is_important", &v) {
			return abort()
		}
		patch.IsImportant = &v
	}

	// Resolve the task's final due date / time / reminder offset: existing
	// values overlaid with whatever this request changes.
	finalDate, finalTime := "", ""
	if existing.DueDate.Valid {
		finalDate = existing.DueDate.Time.Format("2006-01-02")
	}
	if existing.DueTime.Valid {
		finalTime = hhmm(existing.DueTime.String)
	}
	finalOffset := existing.ReminderOffsetMinutes

	if f.has("due_date") {
		var raw *string
		if !f.get(w, "due_date", &raw) {
			return abort()
		}
		if raw == nil || *raw == "" {
			// Clearing the date clears the time and reminder too (service).
			patch.DueDate = &sql.NullTime{}
			finalDate, finalTime = "", ""
		} else {
			d, err := time.Parse("2006-01-02", *raw)
			if err != nil {
				return fail("due_date must be YYYY-MM-DD.")
			}
			patch.DueDate = &sql.NullTime{Time: d, Valid: true}
			finalDate = *raw
		}
	}

	if f.has("due_time") {
		var raw *string
		if !f.get(w, "due_time", &raw) {
			return abort()
		}
		if raw == nil || *raw == "" {
			finalTime = ""
			if finalDate != "" {
				patch.DueTime = &sql.NullString{}
			}
		} else {
			if _, err := time.Parse("15:04", *raw); err != nil {
				return fail("due_time must be HH:MM (24-hour).")
			}
			if finalDate == "" {
				return fail("due_time requires a due_date.")
			}
			patch.DueTime = &sql.NullString{String: *raw, Valid: true}
			finalTime = *raw
		}
	}

	if f.has("reminder_offset_minutes") {
		var n *int
		if !f.get(w, "reminder_offset_minutes", &n) {
			return abort()
		}
		if n == nil {
			finalOffset = sql.NullInt64{}
		} else {
			if _, ok := parseReminderOffset(strconv.Itoa(*n)); !ok {
				return fail("reminder_offset_minutes must be one of 0, 5, 10, 15, 30, 60, 120, 240, 480, 720, 1440.")
			}
			finalOffset = sql.NullInt64{Int64: int64(*n), Valid: true}
		}
	}

	if f.has("due_date") || f.has("due_time") || f.has("reminder_offset_minutes") {
		haveDue := finalDate != "" && finalTime != ""
		switch {
		case !finalOffset.Valid:
			// Explicit reminder removal. (A cleared date/time already wipes
			// the reminder inside the service.)
			if haveDue && existing.ReminderOffsetMinutes.Valid {
				patch.ReminderAt = &sql.NullTime{}
				patch.ReminderOffsetMinutes = &sql.NullInt64{}
			}
		case !haveDue:
			if f.has("reminder_offset_minutes") {
				return fail("A reminder requires both due_date and due_time.")
			}
		default:
			at, err := reminderTime(finalDate, finalTime, int(finalOffset.Int64))
			if err != nil {
				return fail("Couldn't compute the reminder time from due_date and due_time.")
			}
			// Only write when it actually moves: rewriting reminder_at resets
			// the delivered markers, which would re-fire a sent reminder.
			if !existing.ReminderAt.Valid || !existing.ReminderAt.Time.Equal(at) ||
				existing.ReminderOffsetMinutes != finalOffset {
				patch.ReminderAt = &sql.NullTime{Time: at, Valid: true}
				patch.ReminderOffsetMinutes = &finalOffset
			}
		}
	}

	var rec *services.RecurrenceParams
	if f.has("recurrence") {
		if f.isNull("recurrence") {
			rec = &services.RecurrenceParams{} // empty frequency = remove
		} else {
			var in apiRecurrence
			dec := json.NewDecoder(bytes.NewReader(f["recurrence"]))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return fail("recurrence must be an object with frequency, interval, and regeneration_type.")
			}
			p := services.RecurrenceParams{
				Frequency: in.Frequency, Interval: in.Interval, RegenerationType: in.RegenerationType,
			}
			if err := p.Validate(); err != nil {
				return fail(err.Error() + ".")
			}
			rec = &p
		}
	} else if existing.RecurrenceFrequency.Valid &&
		(f.has("title") || f.has("notes") || f.has("project_id") || f.has("is_important") || f.has("due_time")) {
		// Re-apply the current rule so its template snapshots the edit and
		// future instances inherit it. The web form gets this for free by
		// always posting the recurrence fields.
		rec = &services.RecurrenceParams{
			Frequency:        existing.RecurrenceFrequency.String,
			Interval:         int(existing.RecurrenceInterval.Int64),
			RegenerationType: existing.RecurrenceType.String,
		}
	}

	return patch, rec, true
}

// APITaskDelete handles DELETE /api/v1/tasks/{id}.
func (h *Handlers) APITaskDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if err := h.Tasks.Delete(r.Context(), user.ID, chi.URLParam(r, "id")); err != nil {
		h.apiTaskErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APITaskComplete handles POST /api/v1/tasks/{id}/complete. Idempotent.
// next_task is the freshly generated instance when the task recurs.
func (h *Handlers) APITaskComplete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	completed, generated, err := h.Tasks.Complete(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.apiTaskErr(w, err)
		return
	}
	resp := map[string]any{"task": toAPITask(completed), "next_task": nil}
	if generated != nil {
		resp["next_task"] = toAPITask(generated)
	}
	writeJSON(w, http.StatusOK, resp)
}

// APITaskUncomplete handles POST /api/v1/tasks/{id}/uncomplete.
func (h *Handlers) APITaskUncomplete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	task, err := h.Tasks.Uncomplete(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.apiTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITask(task))
}

// APITaskReorder handles POST /api/v1/tasks/reorder with {"ids": [...]}.
func (h *Handlers) APITaskReorder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := h.Tasks.Reorder(r.Context(), user.ID, in.IDs); err != nil {
		apiInternal(w, "reorder tasks", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APISearch handles GET /api/v1/search?q=…&limit=…
func (h *Handlers) APISearch(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			apiError(w, http.StatusBadRequest, "bad_request", "limit must be between 1 and 100.")
			return
		}
		limit = n
	}
	tasks, err := h.Tasks.Search(r.Context(), user.ID, q, limit)
	if err != nil {
		apiInternal(w, "search tasks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": toAPITasks(tasks)})
}

// APIRemindersClaim handles POST /api/v1/reminders/claim. Atomically claims
// every due, undelivered reminder on the client channel (the same one the
// web page's poller uses) and returns the tasks. Each reminder is returned
// exactly once across all clients.
func (h *Handlers) APIRemindersClaim(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	due, err := h.Tasks.PopDueReminders(r.Context(), user.ID)
	if err != nil {
		apiInternal(w, "claim reminders", err)
		return
	}
	out := make([]apiTask, 0, len(due))
	for _, d := range due {
		task, err := h.Tasks.Get(r.Context(), user.ID, d.ID)
		if err != nil {
			apiInternal(w, "load claimed reminder task", err)
			return
		}
		out = append(out, toAPITask(task))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (h *Handlers) writeTask(w http.ResponseWriter, r *http.Request, userID, id string, status int) {
	task, err := h.Tasks.Get(r.Context(), userID, id)
	if err != nil {
		h.apiTaskErr(w, err)
		return
	}
	writeJSON(w, status, toAPITask(task))
}

func (h *Handlers) apiTaskErr(w http.ResponseWriter, err error) {
	if errors.Is(err, services.ErrTaskNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "Task not found.")
		return
	}
	apiInternal(w, "task", err)
}
