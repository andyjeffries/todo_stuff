// Package handlers wires HTTP routes to the auth service and renderer.
package handlers

import (
	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/render"
	"github.com/andyjessop/todostuff/internal/services"
)

type Handlers struct {
	Auth     *auth.Service
	Tasks    *services.Tasks
	Projects *services.Projects
	Render   *render.Renderer
}

func New(a *auth.Service, t *services.Tasks, p *services.Projects, r *render.Renderer) *Handlers {
	return &Handlers{Auth: a, Tasks: t, Projects: p, Render: r}
}
