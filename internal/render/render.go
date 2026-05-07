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
	"formatDueDate": formatDueDate,
	"isOverdue": func(due time.Time) bool {
		today := startOfDay(time.Now())
		return startOfDay(due).Before(today)
	},
	"isToday": func(due time.Time) bool {
		return startOfDay(due).Equal(startOfDay(time.Now()))
	},
	"formatDateInput": func(t time.Time) string { return t.Format("2006-01-02") },
	// maskPushoverKey returns first-4 + middle-bullets + last-4 for at-rest
	// display of a saved Pushover user key. Pushover keys are 30 alphanumeric
	// chars; for anything <=8 chars we just bullet the whole thing.
	"maskPushoverKey": func(s string) string {
		if len(s) <= 8 {
			return strings.Repeat("•", len(s))
		}
		return s[:4] + strings.Repeat("•", len(s)-8) + s[len(s)-4:]
	},
	// dict builds a map[string]any from alternating key/value pairs, so
	// templates can pass multi-field data when invoking sub-templates.
	"dict": dictFunc,
}

// startOfDay returns midnight in t's own location. Day comparisons must
// happen at the same wall-clock granularity as the user, not UTC.
func startOfDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// formatDueDate produces a Things-3-style label for a due date:
//
//	Yesterday / Today / Tomorrow within ±1 day,
//	weekday name within the next 6 days (e.g. "Friday"),
//	"3 May" for past or further future dates within the same year,
//	"3 May 2027" beyond.
func formatDueDate(due time.Time) string {
	today := startOfDay(time.Now())
	d := startOfDay(due)
	delta := int(d.Sub(today).Hours() / 24)
	switch {
	case delta == 0:
		return "Today"
	case delta == 1:
		return "Tomorrow"
	case delta == -1:
		return "Yesterday"
	case delta > 1 && delta <= 6:
		return d.Format("Monday")
	}
	if d.Year() == today.Year() {
		return d.Format("2 Jan")
	}
	return d.Format("2 Jan 2006")
}

func dictFunc(values ...any) (map[string]any, error) {
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
}
