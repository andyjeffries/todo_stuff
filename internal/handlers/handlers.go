// Package handlers wires HTTP routes to the auth service and renderer.
package handlers

import (
	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/render"
	"github.com/andyjessop/todostuff/internal/services"
)

type Handlers struct {
	Auth   *auth.Service
	Tasks  *services.Tasks
	Render *render.Renderer
}

func New(a *auth.Service, t *services.Tasks, r *render.Renderer) *Handlers {
	return &Handlers{Auth: a, Tasks: t, Render: r}
}
