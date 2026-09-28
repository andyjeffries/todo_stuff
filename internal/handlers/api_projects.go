package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// APIProjectList handles GET /api/v1/projects, in sidebar order.
func (h *Handlers) APIProjectList(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	projects, err := h.Projects.List(r.Context(), user.ID)
	if err != nil {
		apiInternal(w, "list projects", err)
		return
	}
	out := make([]apiProject, 0, len(projects))
	for i := range projects {
		out = append(out, toAPIProject(&projects[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

// APIProjectGet handles GET /api/v1/projects/{id}.
func (h *Handlers) APIProjectGet(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	pr, err := h.Projects.Get(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.apiProjectErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIProject(pr))
}

// APIProjectCreate handles POST /api/v1/projects. New projects go to the end
// of the sidebar.
func (h *Handlers) APIProjectCreate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		Name string  `json:"name"`
		Icon *string `json:"icon"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		apiInvalid(w, "name is required.")
		return
	}
	p := services.CreateProjectParams{Name: in.Name}
	if in.Icon != nil {
		p.Icon = *in.Icon
	}
	pr, err := h.Projects.Create(r.Context(), user.ID, p)
	if err != nil {
		apiInternal(w, "create project", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPIProject(pr))
}

// APIProjectUpdate handles PATCH /api/v1/projects/{id}: name and/or icon.
func (h *Handlers) APIProjectUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	f, ok := decodeFields(w, r, "name", "icon")
	if !ok {
		return
	}
	var patch services.ProjectPatch
	if f.has("name") {
		var name string
		if f.isNull("name") {
			apiInvalid(w, "name cannot be null.")
			return
		}
		if !f.get(w, "name", &name) {
			return
		}
		if strings.TrimSpace(name) == "" {
			apiInvalid(w, "name cannot be empty.")
			return
		}
		patch.Name = &name
	}
	if f.has("icon") {
		var icon *string
		if !f.get(w, "icon", &icon) {
			return
		}
		v := ""
		if icon != nil {
			v = strings.TrimSpace(*icon)
		}
		patch.Icon = &sql.NullString{String: v, Valid: v != ""}
	}
	pr, err := h.Projects.Update(r.Context(), user.ID, chi.URLParam(r, "id"), patch)
	if err != nil {
		h.apiProjectErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPIProject(pr))
}

// APIProjectDelete handles DELETE /api/v1/projects/{id}. The project's tasks
// are kept and fall back to having no project.
func (h *Handlers) APIProjectDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if err := h.Projects.Delete(r.Context(), user.ID, chi.URLParam(r, "id")); err != nil {
		h.apiProjectErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APIProjectReorder handles POST /api/v1/projects/reorder with {"ids": [...]}.
func (h *Handlers) APIProjectReorder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := h.Projects.Reorder(r.Context(), user.ID, in.IDs); err != nil {
		apiInternal(w, "reorder projects", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APIProjectMove handles POST /api/v1/projects/{id}/move with
// {"direction": "up"|"down"}: swaps with the neighbouring project.
func (h *Handlers) APIProjectMove(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var in struct {
		Direction string `json:"direction"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	dir := services.MoveDirection(in.Direction)
	if dir != services.MoveUp && dir != services.MoveDown {
		apiInvalid(w, `direction must be "up" or "down".`)
		return
	}
	if err := h.Projects.Move(r.Context(), user.ID, chi.URLParam(r, "id"), dir); err != nil {
		h.apiProjectErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) apiProjectErr(w http.ResponseWriter, err error) {
	if errors.Is(err, services.ErrProjectNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "Project not found.")
		return
	}
	apiInternal(w, "project", err)
}
