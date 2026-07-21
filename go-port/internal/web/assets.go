// Package web ports pwnagotchi/ui/web/*.py: the Flask-based web UI
// (index/device-frame, pwnmail inbox, plugin manager) — real net/http
// serving real embedded assets, not a stub.
package web

import (
	"embed"
	"html/template"
)

//go:embed static
var staticFS embed.FS

//go:embed templates/*.tmpl
var templateFS embed.FS

var funcMap = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
}

// baseTemplate is the parsed base.tmpl (the "base" entrypoint plus its
// default block bodies) — the layout every page template clones and
// overrides blocks in, mirroring Jinja's `{% extends "base.html" %}`.
var baseTemplate = template.Must(template.New("base").Funcs(funcMap).ParseFS(templateFS, "templates/base.tmpl"))

// statusTemplate is the standalone (non-extending) status.tmpl.
var statusTemplate = template.Must(template.New("status").Funcs(funcMap).ParseFS(templateFS, "templates/status.tmpl"))

// pageTemplate clones the base layout and parses in one page's block
// overrides (title/content/script, and for plugins.tmpl: scripts too),
// then executes the "base" entrypoint — the Go equivalent of Jinja
// rendering a child template that `{% extends "base.html" %}`.
func pageTemplate(name string) *template.Template {
	t := template.Must(baseTemplate.Clone())
	return template.Must(t.ParseFS(templateFS, "templates/"+name+".tmpl"))
}

var pageTemplates = map[string]*template.Template{
	"index":       pageTemplate("index"),
	"inbox":       pageTemplate("inbox"),
	"message":     pageTemplate("message"),
	"new_message": pageTemplate("new_message"),
	"peers":       pageTemplate("peers"),
	"profile":     pageTemplate("profile"),
	"plugins":     pageTemplate("plugins"),
}

func renderTemplate(name string, data map[string]interface{}) ([]byte, error) {
	t, ok := pageTemplates[name]
	if !ok {
		panic("web: unknown template " + name)
	}
	var buf writeBuffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		return nil, err
	}
	return buf.b, nil
}

func renderStatus(data map[string]interface{}) ([]byte, error) {
	var buf writeBuffer
	if err := statusTemplate.ExecuteTemplate(&buf, "status", data); err != nil {
		return nil, err
	}
	return buf.b, nil
}

// writeBuffer is a minimal io.Writer sink (avoids importing bytes just for
// a Buffer in this file).
type writeBuffer struct{ b []byte }

func (w *writeBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
