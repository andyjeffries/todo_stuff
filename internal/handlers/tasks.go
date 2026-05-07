package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// taskDetailData wraps a task plus the user's projects for the detail-panel
// project selector. Rule is non-nil when the task has a recurrence rule
// attached, so the detail-panel form can pre-select frequency/interval/type.
type taskDetailData struct {
	Task     models.Task
	Projects []models.Project
	Rule     *models.RecurrenceRule
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

	if rec, ok := readRecurrenceFromForm(r); ok {
		if err := h.Tasks.SetRecurrenceForTask(r.Context(), user.ID, task.ID, rec); err != nil {
			slog.Error("set recurrence on create", "err", err)
			http.Error(w, "could not set recurrence", http.StatusBadRequest)
			return
		}
		// Re-fetch so the rendered row reflects RecurrenceRuleID.
		if refreshed, err := h.Tasks.Get(r.Context(), user.ID, task.ID); err == nil {
			task = refreshed
		}
	}

	row := taskRowData{Task: *task, HideProject: hideProjectForRequest(r)}
	if err := h.Render.Render(w, http.StatusOK, "today", "task-row", row); err != nil {
		slog.Error("render task-row", "err", err)
	}
}

// readRecurrenceFromForm pulls recurrence_* fields off the form. Returns
// (params, true) when at least one recurrence field is present (so the
// caller can apply or clear). Returns (_, false) when no recurrence intent
// is on this request — the caller should leave any existing rule alone.
func readRecurrenceFromForm(r *http.Request) (services.RecurrenceParams, bool) {
	hasFreq := r.PostForm.Has("recurrence_frequency")
	hasInterval := r.PostForm.Has("recurrence_interval")
	hasType := r.PostForm.Has("recurrence_type")
	hasPresent := r.PostForm.Has("recurrence_present")
	if !hasFreq && !hasInterval && !hasType && !hasPresent {
		return services.RecurrenceParams{}, false
	}
	interval, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("recurrence_interval")))
	return services.RecurrenceParams{
		Frequency:        strings.TrimSpace(r.PostFormValue("recurrence_frequency")),
		Interval:         interval,
		RegenerationType: strings.TrimSpace(r.PostFormValue("recurrence_type")),
	}, true
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
	if task.RecurrenceRuleID.Valid {
		rule, err := h.Tasks.GetRule(r.Context(), user.ID, task.RecurrenceRuleID.String)
		if err != nil && !errors.Is(err, services.ErrRuleNotFound) {
			slog.Error("get recurrence rule for task detail", "err", err)
		}
		data.Rule = rule
	}
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
	// Reminder = (due_date + due_time) − offset_minutes. The form posts an
	// offset selected from a curated dropdown; the server computes the
	// absolute reminder_at so the polling query stays a simple WHERE clause.
	// A reminder requires both a due date AND a due time; clearing either
	// (handled in the service Update) wipes the reminder columns.
	if r.PostForm.Has("reminder_offset_present") {
		offsetRaw := strings.TrimSpace(r.PostFormValue("reminder_offset"))
		// Pick the *final* due date/time as the form sees it (the same form
		// posts due_date / due_time alongside the offset). Fall back to the
		// existing patch values when the form doesn't include the markers.
		dueDateRaw := strings.TrimSpace(r.PostFormValue("due_date"))
		dueTimeRaw := strings.TrimSpace(r.PostFormValue("due_time"))

		if offsetRaw == "" || dueDateRaw == "" || dueTimeRaw == "" {
			patch.ReminderAt = &sql.NullTime{}
			patch.ReminderOffsetMinutes = &sql.NullInt64{}
		} else if minutes, ok := parseReminderOffset(offsetRaw); ok {
			if dueAt, err := time.ParseInLocation("2006-01-02 15:04", dueDateRaw+" "+dueTimeRaw, time.Local); err == nil {
				reminderAt := dueAt.Add(-time.Duration(minutes) * time.Minute).UTC()
				patch.ReminderAt = &sql.NullTime{Time: reminderAt, Valid: true}
				patch.ReminderOffsetMinutes = &sql.NullInt64{Int64: int64(minutes), Valid: true}
			}
		}
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

	// Apply any recurrence changes after the field update so the rule's
	// template_* fields snapshot the *new* task state (renaming the task
	// renames future instances too).
	if rec, ok := readRecurrenceFromForm(r); ok {
		if err := h.Tasks.SetRecurrenceForTask(r.Context(), user.ID, task.ID, rec); err != nil {
			slog.Error("set recurrence on update", "err", err)
			http.Error(w, "could not update recurrence", http.StatusBadRequest)
			return
		}
		if refreshed, err := h.Tasks.Get(r.Context(), user.ID, task.ID); err == nil {
			task = refreshed
		}
	}

	// OOB response. Always includes a notes-display OOB swap (keeps the
	// detail panel's rendered preview in sync with the textarea source), plus
	// either an in-place row replace (the task still belongs to this view) or
	// an OOB delete (the update pushed it out — e.g. setting a date on an
	// Inbox task, or moving a Today task to tomorrow).
	hxURL := r.Header.Get("Hx-Current-Url")
	resp := taskUpdateResponse{
		Row:     taskRowData{Task: *task, HideProject: hideProjectForRequest(r)},
		Removed: !taskBelongsToCurrentView(task, hxURL, time.Now()),
	}
	if err := h.Render.Render(w, http.StatusOK, "today", "task-update-response", resp); err != nil {
		slog.Error("render task-update-response", "err", err)
	}
}

// taskUpdateResponse is the wrapper shape the task-update-response template
// expects. Removed=true sends an OOB delete instead of a row replace.
type taskUpdateResponse struct {
	Row     taskRowData
	Removed bool
}

// TaskComplete handles POST /tasks/{id}/complete. Returns an empty 200 so
// the calling list row is swapped out of the DOM. The detail panel (if
// open against this task) is closed by the row form's onclick handler.
//
// When the task is recurring and a new instance was generated, sets the
// HX-Trigger response header so the client can refresh the active task
// list (the new instance may belong to the current view, e.g. a daily
// recurrence on /today).
func (h *Handlers) TaskComplete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	_, generated, err := h.Tasks.Complete(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, services.ErrTaskNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("complete task", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if generated != nil {
		w.Header().Set("HX-Trigger", "tasks-list-changed")
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

// TaskReorder handles POST /tasks/reorder. The form posts the new task
// order as repeated `ids` fields (in the order SortableJS reports after the
// drop). Returns 204 on success — the client has already moved the DOM
// nodes optimistically, so there's nothing to render.
func (h *Handlers) TaskReorder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ids := r.PostForm["ids"]
	if len(ids) == 0 {
		// Some clients post comma-joined ids as a single field. Accept both.
		if joined := strings.TrimSpace(r.PostFormValue("ids")); joined != "" {
			ids = strings.Split(joined, ",")
		}
	}
	if err := h.Tasks.Reorder(r.Context(), user.ID, ids); err != nil {
		slog.Error("reorder tasks", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// allowedReminderOffsets is the closed set of offset values the dropdown
// surfaces. Server-side validation rejects anything outside this set so a
// crafted POST can't store an arbitrary value.
var allowedReminderOffsets = map[int]struct{}{
	0: {}, 5: {}, 10: {}, 15: {}, 30: {}, 60: {},
	120: {}, 240: {}, 480: {}, 720: {}, 1440: {},
}

func parseReminderOffset(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if _, ok := allowedReminderOffsets[n]; !ok {
		return 0, false
	}
	return n, true
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}
