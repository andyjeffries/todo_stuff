package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/andyjessop/todostuff/internal/auth"
)

// dueReminderJSON is the wire shape returned by /api/reminders/due. Plain
// fields, plain JSON — keep this stable so the JS poller can stay simple.
type dueReminderJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Notes string `json:"notes,omitempty"`
}

// RemindersDue handles GET /api/reminders/due. Returns a JSON array of
// reminders whose reminder_at has passed and which haven't been delivered
// yet, then atomically marks them delivered so the next poll won't repeat
// them. Empty JSON array (`[]`), not `null`, when there's nothing to send —
// the frontend can iterate without a guard.
func (h *Handlers) RemindersDue(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := h.Tasks.PopDueReminders(r.Context(), user.ID)
	if err != nil {
		slog.Error("pop due reminders", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]dueReminderJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, dueReminderJSON{ID: r.ID, Title: r.Title, Notes: r.Notes})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("encode due reminders", "err", err)
	}
}
