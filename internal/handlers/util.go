package handlers

import "html/template"

func htmlEscape(s string) string {
	return template.HTMLEscapeString(s)
}
