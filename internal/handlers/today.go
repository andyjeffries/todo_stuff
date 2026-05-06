package handlers

import (
	"fmt"
	"net/http"

	"github.com/andyjessop/todostuff/internal/auth"
)

// Today is a placeholder for the Today smart list. Milestone 5 replaces this
// with the real templated view.
func (h *Handlers) Today(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Today · TodoStuff</title>
<h1>Today</h1><p>Signed in as %s.</p>
<form method="POST" action="/logout"><button type="submit">Sign out</button></form>`,
		htmlEscape(user.Name))
}
