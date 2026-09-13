package web

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strings"
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

var funcMap = template.FuncMap{
	"money": money,
}

// money formats a float64 as a comma-grouped decimal with exactly two places
// (e.g. 48000 -> "48,000.00", -1234.5 -> "-1,234.50"). Templates still
// supply the currency symbol themselves (₦{{money .X}}) — this only adds
// the digit grouping printf "%.2f" doesn't do on its own.
func money(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	n := len(whole)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(whole[i])
	}
	b.WriteByte('.')
	b.WriteString(frac)

	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Render executes layout.html + partials/nav.html + the given page (plus any
// extraPartials the page references via {{template "name.html" .}}, e.g. a
// fragment file also reused standalone via RenderFragment) against data,
// writing status and the result to w. page is relative to the templates dir,
// e.g. "auth/login.html".
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, data any, extraPartials ...string) error {
	return r.RenderWithLayout(w, status, "layout.html", page, data, append([]string{filepath.Join("partials", "nav.html")}, extraPartials...)...)
}

// RenderWithLayout is Render with the layout file itself as a parameter
// instead of always assuming layout.html+partials/nav.html — the customer
// portal uses this with its own portal/layout.html so external contacts
// never see the internal app's nav.
func (r *Renderer) RenderWithLayout(w http.ResponseWriter, status int, layout, page string, data any, extraPartials ...string) error {
	files := []string{
		filepath.Join(r.dir, layout),
		filepath.Join(r.dir, page),
	}
	for _, p := range extraPartials {
		files = append(files, filepath.Join(r.dir, p))
	}
	tmpl, err := template.New(filepath.Base(files[0])).Funcs(funcMap).ParseFiles(files...)
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
	tmpl, err := template.New(filepath.Base(path)).Funcs(funcMap).ParseFiles(path)
	if err != nil {
		return err
	}
	return tmpl.ExecuteTemplate(w, filepath.Base(path), data)
}
