package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/models"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/go-chi/chi/v5"
)

// projectViewData drives the /projects/{id} page.
type projectViewData struct {
	appViewData
	Project models.Project
}

// projectListData drives the sidebar projects list partial.
type projectListData struct {
	Projects        []models.Project
	ActiveProjectID string
}

// activeProjectID extracts the current project context from the
// `Hx-Current-Url` header HTMX sends with every request, so server-rendered
// partials can preserve the active highlight without the client passing it
// explicitly. Returns "" if not on a project page.
func activeProjectIDFromRequest(r *http.Request) string {
	const prefix = "/projects/"
	url := r.Header.Get("Hx-Current-Url")
	if url == "" {
		return ""
	}
	idx := strings.Index(url, prefix)
	if idx < 0 {
		return ""
	}
	rest := url[idx+len(prefix):]
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		rest = rest[:cut]
	}
	return rest
}

// ProjectCreate handles POST /projects. Returns the rendered sidebar projects
// list partial so HTMX can swap it in.
func (h *Handlers) ProjectCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if _, err := h.Projects.Create(r.Context(), user.ID, services.CreateProjectParams{
		Name: r.PostFormValue("name"),
		Icon: r.PostFormValue("icon"),
	}); err != nil {
		slog.Error("create project", "err", err)
		http.Error(w, "could not create project", http.StatusBadRequest)
		return
	}

	h.renderProjectList(w, r, user.ID)
}

// ProjectView handles GET /projects/{id}.
func (h *Handlers) ProjectView(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")

	pr, err := h.Projects.Get(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, services.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("get project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	tasks, err := h.Tasks.ListByProject(r.Context(), user.ID, id)
	if err != nil {
		slog.Error("list project tasks", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := projectViewData{
		appViewData: appViewData{
			Title:           pr.Name,
			ActiveProjectID: pr.ID,
			Heading:         pr.Name,
			User:            user,
			Tasks:           tasks,
			Projects:        h.loadSidebarProjects(r, user.ID),
			HideProject:     true,
		},
		Project: *pr,
	}
	if err := h.Render.Render(w, http.StatusOK, "project", "app", data); err != nil {
		slog.Error("render project view", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ProjectUpdate handles PUT /projects/{id}. Returns the updated header
// partial plus an OOB-swap of the sidebar list, so a rename is reflected
// in both places without a page reload.
func (h *Handlers) ProjectUpdate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var patch services.ProjectPatch
	if r.PostForm.Has("name") {
		name := r.PostFormValue("name")
		patch.Name = &name
	}
	if r.PostForm.Has("icon") {
		icon := strings.TrimSpace(r.PostFormValue("icon"))
		patch.Icon = &sql.NullString{String: icon, Valid: icon != ""}
	}

	pr, err := h.Projects.Update(r.Context(), user.ID, id, patch)
	if err != nil {
		if errors.Is(err, services.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("update project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	projects, err := h.Projects.List(r.Context(), user.ID)
	if err != nil {
		slog.Error("list projects after update", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.Render.Render(w, http.StatusOK, "project", "project-update-response", projectUpdateData{
		Project: *pr,
		ProjectList: projectListData{
			Projects:        projects,
			ActiveProjectID: pr.ID,
		},
	}); err != nil {
		slog.Error("render project-update-response", "err", err)
	}
}

type projectUpdateData struct {
	Project     models.Project
	ProjectList projectListData
}

// ProjectDelete handles DELETE /projects/{id}. On success, instructs HTMX to
// redirect to /today via the HX-Redirect header.
func (h *Handlers) ProjectDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.Projects.Delete(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, services.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("delete project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Redirect", "/today")
	w.WriteHeader(http.StatusOK)
}

// ProjectMove handles POST /projects/{id}/move?dir=up|down. Returns the
// updated sidebar projects list partial.
func (h *Handlers) ProjectMove(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	dir := services.MoveDirection(r.URL.Query().Get("dir"))
	if dir != services.MoveUp && dir != services.MoveDown {
		http.Error(w, "invalid direction", http.StatusBadRequest)
		return
	}

	if err := h.Projects.Move(r.Context(), user.ID, id, dir); err != nil {
		if errors.Is(err, services.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("move project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderProjectList(w, r, user.ID)
}

// renderProjectList writes the sidebar projects list partial into w.
func (h *Handlers) renderProjectList(w http.ResponseWriter, r *http.Request, userID string) {
	projects, err := h.Projects.List(r.Context(), userID)
	if err != nil {
		slog.Error("list projects", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := projectListData{
		Projects:        projects,
		ActiveProjectID: activeProjectIDFromRequest(r),
	}
	if err := h.Render.Render(w, http.StatusOK, "today", "project-list", data); err != nil {
		slog.Error("render project-list", "err", err)
	}
}

