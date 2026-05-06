// Package handlers wires HTTP routes to the auth service and renderer.
package handlers

import (
	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/render"
)

type Handlers struct {
	Auth   *auth.Service
	Render *render.Renderer
}

func New(a *auth.Service, r *render.Renderer) *Handlers {
	return &Handlers{Auth: a, Render: r}
}
