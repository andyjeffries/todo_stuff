// Package web exposes the embedded HTML templates that ship with the binary.
package web

import "embed"

//go:embed all:templates
var TemplateFS embed.FS
