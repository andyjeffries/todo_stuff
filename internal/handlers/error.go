package handlers

import (
	"log/slog"
	"net/http"

	appmw "github.com/andyjessop/todostuff/internal/middleware"
)

// errorViewData feeds web/templates/pages/error.html. The auth layout reads
// Title/Subtitle (subtitle is unused for errors but the layout dereferences
// it via {{with .Subtitle}} so an empty string is fine).
type errorViewData struct {
	Title    string
	Theme    string
	Subtitle string
	Status   int
	Message  string
}

func (h *Handlers) renderError(w http.ResponseWriter, r *http.Request, status int, title, msg string) {
	data := errorViewData{Title: title, Status: status, Message: msg, Theme: appmw.ThemeFromContext(r.Context())}
	if err := h.Render.Render(w, status, "error", "auth", data); err != nil {
		// Last-ditch fallback: don't render a half-written page if templates
		// themselves are broken. Send the bare status — better than nothing.
		slog.Error("render error page", "err", err, "status", status)
		http.Error(w, msg, status)
	}
}

// NotFound renders the friendly 404 page. Wired via chi's r.NotFound.
func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, r, http.StatusNotFound,
		"Page not found",
		"That URL isn't anywhere we recognise.")
}

// MethodNotAllowed renders the friendly 405 page. Wired via chi's
// r.MethodNotAllowed.
func (h *Handlers) MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, r, http.StatusMethodNotAllowed,
		"Not allowed",
		"That method isn't supported on this URL.")
}

// InternalError renders the friendly 500 page. Used by the recover middleware
// (see internal/middleware/recover.go) when a handler panics.
func (h *Handlers) InternalError(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, r, http.StatusInternalServerError,
		"Something went wrong",
		"Something on our side broke. We've logged it.")
}
