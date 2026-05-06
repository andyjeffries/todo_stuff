package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
)

// appViewData drives any page rendered against the "app" layout.
type appViewData struct {
	Title      string
	ActiveView string
	Heading    string
	Subheading string
	User       *models.User
	Tasks      []models.Task
}

func (h *Handlers) renderAppView(w http.ResponseWriter, r *http.Request, page string, data appViewData) {
	if data.User == nil {
		if u, ok := auth.UserFromContext(r.Context()); ok {
			data.User = u
		}
	}
	if err := h.Render.Render(w, http.StatusOK, page, "app", data); err != nil {
		slog.Error("render app view", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) listForView(r *http.Request, view services.View) []models.Task {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil
	}
	tasks, err := h.Tasks.ListByView(r.Context(), user.ID, view)
	if err != nil {
		slog.Error("list tasks", "view", view, "err", err)
		return nil
	}
	return tasks
}

func (h *Handlers) Today(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "today", appViewData{
		Title:      "Today",
		ActiveView: "today",
		Heading:    "Today",
		Subheading: "Tasks due today and anything left undated.",
		Tasks:      h.listForView(r, services.ViewToday),
	})
}

func (h *Handlers) Inbox(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "inbox", appViewData{
		Title:      "Inbox",
		ActiveView: "inbox",
		Heading:    "Inbox",
		Subheading: "Quick captures with no project and no due date.",
		Tasks:      h.listForView(r, services.ViewInbox),
	})
}

func (h *Handlers) Upcoming(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "upcoming", appViewData{
		Title:      "Upcoming",
		ActiveView: "upcoming",
		Heading:    "Upcoming",
		Subheading: "Tasks scheduled for a future date.",
		Tasks:      h.listForView(r, services.ViewUpcoming),
	})
}

func (h *Handlers) Anytime(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "anytime", appViewData{
		Title:      "Anytime",
		ActiveView: "anytime",
		Heading:    "Anytime",
		Subheading: "Every open task across your projects.",
		Tasks:      h.listForView(r, services.ViewAnytime),
	})
}

// LogbookGroup is a single completion-date bucket for the Logbook view.
type LogbookGroup struct {
	Label string
	Tasks []models.Task
}

type logbookViewData struct {
	appViewData
	Groups []LogbookGroup
}

func (h *Handlers) Logbook(w http.ResponseWriter, r *http.Request) {
	tasks := h.listForView(r, services.ViewLogbook)
	data := logbookViewData{
		appViewData: appViewData{
			Title:      "Logbook",
			ActiveView: "logbook",
			Heading:    "Logbook",
			Subheading: "Completed tasks, grouped by the day you finished them.",
			Tasks:      tasks,
		},
		Groups: groupLogbook(tasks, time.Now()),
	}
	if u, ok := auth.UserFromContext(r.Context()); ok {
		data.User = u
	}
	if err := h.Render.Render(w, http.StatusOK, "logbook", "app", data); err != nil {
		slog.Error("render app view", "page", "logbook", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// groupLogbook buckets completed tasks by their local completion date.
// Tasks are assumed already ordered by completed_at DESC, which preserves
// both inter-group and intra-group ordering.
func groupLogbook(tasks []models.Task, now time.Time) []LogbookGroup {
	if len(tasks) == 0 {
		return nil
	}
	loc := now.Location()
	today := dayKey(now.In(loc))
	yesterday := dayKey(now.In(loc).AddDate(0, 0, -1))

	var groups []LogbookGroup
	var current *LogbookGroup
	var currentKey string

	for _, task := range tasks {
		if !task.CompletedAt.Valid {
			continue
		}
		local := task.CompletedAt.Time.In(loc)
		key := dayKey(local)
		if current == nil || key != currentKey {
			label := local.Format("Mon, 2 Jan 2006")
			switch key {
			case today:
				label = "Today"
			case yesterday:
				label = "Yesterday"
			}
			groups = append(groups, LogbookGroup{Label: label})
			current = &groups[len(groups)-1]
			currentKey = key
		}
		current.Tasks = append(current.Tasks, task)
	}
	return groups
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }
