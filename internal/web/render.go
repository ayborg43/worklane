package web

import (
	"html/template"
	"io"
	"net/http"
	"path/filepath"
)

// Renderer renders full pages (layout + nav partial + one page template) and
// standalone HTML fragments (for htmx partial swaps). Templates are parsed
// fresh on every call rather than cached — this app is small enough that the
// disk-read cost is negligible, and it avoids ever serving a stale template
// during development.
type Renderer struct {
	dir string // e.g. "web/templates"
}

func NewRenderer(dir string) *Renderer {
	return &Renderer{dir: dir}
}

// Render executes layout.html + partials/nav.html + the given page (plus any
// extraPartials the page references via {{template "name.html" .}}, e.g. a
// fragment file also reused standalone via RenderFragment) against data,
// writing status and the result to w. page is relative to the templates dir,
// e.g. "auth/login.html".
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, data any, extraPartials ...string) error {
	files := []string{
		filepath.Join(r.dir, "layout.html"),
		filepath.Join(r.dir, "partials", "nav.html"),
		filepath.Join(r.dir, page),
	}
	for _, p := range extraPartials {
		files = append(files, filepath.Join(r.dir, p))
	}
	tmpl, err := template.ParseFiles(files...)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return tmpl.ExecuteTemplate(w, "layout", data)
}

// RenderFragment executes a single standalone template file (no layout) —
// used for htmx-swapped fragments like a task row or a chat message.
func (r *Renderer) RenderFragment(w http.ResponseWriter, status int, page string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return r.RenderFragmentTo(w, page, data)
}

// RenderFragmentTo is RenderFragment without the HTTP response semantics —
// used when the rendered bytes need to go somewhere other than a single
// http.ResponseWriter, e.g. chat broadcasts the same rendered message to
// every WebSocket client in a channel.
func (r *Renderer) RenderFragmentTo(w io.Writer, page string, data any) error {
	path := filepath.Join(r.dir, page)
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		return err
	}
	return tmpl.ExecuteTemplate(w, filepath.Base(path), data)
}
