// Package render parses the embedded HTML templates and offers a tiny helper
// for executing them against a response writer.
//
// Each page in templates/pages/ becomes its own *template.Template with all
// layouts and partials parsed alongside it. The caller picks which top-level
// layout block to execute by name (e.g. "auth", "app").
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

type Renderer struct {
	pages map[string]*template.Template
}

func New(fsys fs.FS) (*Renderer, error) {
	layoutFiles, err := fs.Glob(fsys, "templates/layouts/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob layouts: %w", err)
	}
	partialFiles, err := fs.Glob(fsys, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob partials: %w", err)
	}
	pageFiles, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob pages: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, fmt.Errorf("render: no page templates found")
	}

	r := &Renderer{pages: make(map[string]*template.Template, len(pageFiles))}
	for _, p := range pageFiles {
		files := []string{p}
		files = append(files, layoutFiles...)
		files = append(files, partialFiles...)

		tpl, err := template.New("").Funcs(funcMap).ParseFS(fsys, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}

		name := strings.TrimSuffix(path.Base(p), ".html")
		r.pages[name] = tpl
	}
	return r, nil
}

// Render executes the named layout from the page template tree. Output is
// buffered so a template error never leaves a half-written response.
func (r *Renderer) Render(w http.ResponseWriter, status int, page, layout string, data any) error {
	tpl, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("render: unknown page %q", page)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, layout, data); err != nil {
		return fmt.Errorf("execute %s/%s: %w", page, layout, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

var funcMap = template.FuncMap{
	"safeHTML": func(s string) template.HTML { return template.HTML(s) },
	"formatDate": func(t time.Time) string {
		return t.Format("Mon, 2 Jan 2006")
	},
	"formatTime": func(s string) string {
		// Accept "HH:MM" or "HH:MM:SS"; render as "3:04 PM".
		layouts := []string{"15:04:05", "15:04"}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed.Format("3:04 PM")
			}
		}
		return s
	},
	// dict builds a map[string]any from alternating key/value pairs, so
	// templates can pass multi-field data when invoking sub-templates.
	"dict": func(values ...any) (map[string]any, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict: odd number of arguments")
		}
		out := make(map[string]any, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: key %d is not a string", i)
			}
			out[key] = values[i+1]
		}
		return out, nil
	},
}
