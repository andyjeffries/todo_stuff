// Package handlers wires HTTP routes to the auth service and renderer.
package handlers

import (
	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/notifications"
	"github.com/andyjessop/todostuff/internal/render"
	"github.com/andyjessop/todostuff/internal/services"
)

type Handlers struct {
	Auth     *auth.Service
	Tasks    *services.Tasks
	Projects *services.Projects
	Pushover *notifications.Pushover
	Render   *render.Renderer
}

func New(a *auth.Service, t *services.Tasks, p *services.Projects, pu *notifications.Pushover, r *render.Renderer) *Handlers {
	return &Handlers{Auth: a, Tasks: t, Projects: p, Pushover: pu, Render: r}
}
