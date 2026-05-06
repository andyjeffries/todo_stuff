package handlers

import (
	"log/slog"
	"net/http"

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

func (h *Handlers) Logbook(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "logbook", appViewData{
		Title:      "Logbook",
		ActiveView: "logbook",
		Heading:    "Logbook",
		Subheading: "Completed tasks, grouped by the day you finished them.",
		Tasks:      h.listForView(r, services.ViewLogbook),
	})
}
