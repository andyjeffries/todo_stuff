package handlers

import (
	"log/slog"
	"net/http"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
)

// appViewData drives any page rendered against the "app" layout.
type appViewData struct {
	Title      string
	ActiveView string
	Heading    string
	Subheading string
	User       *models.User
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

func (h *Handlers) Today(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "today", appViewData{
		Title:      "Today",
		ActiveView: "today",
		Heading:    "Today",
		Subheading: "Tasks due today and anything left undated.",
	})
}

func (h *Handlers) Inbox(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "inbox", appViewData{
		Title:      "Inbox",
		ActiveView: "inbox",
		Heading:    "Inbox",
		Subheading: "Quick captures that haven't been filed into a project yet.",
	})
}

func (h *Handlers) Upcoming(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "upcoming", appViewData{
		Title:      "Upcoming",
		ActiveView: "upcoming",
		Heading:    "Upcoming",
		Subheading: "Tasks scheduled for a future date.",
	})
}

func (h *Handlers) Anytime(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "anytime", appViewData{
		Title:      "Anytime",
		ActiveView: "anytime",
		Heading:    "Anytime",
		Subheading: "Every open task across your projects.",
	})
}

func (h *Handlers) Logbook(w http.ResponseWriter, r *http.Request) {
	h.renderAppView(w, r, "logbook", appViewData{
		Title:      "Logbook",
		ActiveView: "logbook",
		Heading:    "Logbook",
		Subheading: "Completed tasks, grouped by the day you finished them.",
	})
}
