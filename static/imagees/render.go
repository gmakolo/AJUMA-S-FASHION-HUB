package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The templates and the stylesheet ship inside the binary, so a deployment is
// one file with a data directory beside it.
//
//go:embed templates static
var embedded embed.FS

// Renderer parses each page against its layout once at boot. With -dev it
// re-reads from disk on every request instead, so the owner can adjust
// wording or styling without a rebuild.
type Renderer struct {
	fsys fs.FS
	dev  bool

	mu    sync.RWMutex
	pages map[string]*template.Template
}

// NewRenderer parses the template set.
func NewRenderer(fsys fs.FS, dev bool) (*Renderer, error) {
	r := &Renderer{fsys: fsys, dev: dev}
	pages, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.pages = pages
	return r, nil
}

// parse pairs every file in templates/pages with a layout: admin pages with
// the admin chrome, everything else with the storefront.
func (r *Renderer) parse() (map[string]*template.Template, error) {
	pages, err := fs.Glob(r.fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no templates found under templates/pages")
	}
	partials, err := fs.Glob(r.fsys, "templates/partials/*.html")
	if err != nil {
		return nil, err
	}

	out := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		name := path.Base(page)
		layout := "templates/layouts/site.html"
		if strings.HasPrefix(name, "admin") {
			layout = "templates/layouts/admin.html"
		}
		files := append([]string{layout, page}, partials...)
		tmpl, err := template.New(path.Base(layout)).Funcs(templateFuncs).ParseFS(r.fsys, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		out[name] = tmpl
	}
	return out, nil
}

// lookup finds a parsed page, re-parsing the whole set first in dev mode.
func (r *Renderer) lookup(name string) (*template.Template, error) {
	if r.dev {
		pages, err := r.parse()
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.pages = pages
		r.mu.Unlock()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tmpl, ok := r.pages[name]
	if !ok {
		return nil, fmt.Errorf("template %q is not registered", name)
	}
	return tmpl, nil
}

// Render writes a page. The template is executed into a buffer first: a
// template that fails halfway must not leave a torn page behind a 200.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, data any) {
	tmpl, err := r.lookup(name)
	if err != nil {
		slog.Error("template lookup failed", "page", name, "error", err)
		http.Error(w, "This page is temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		slog.Error("template render failed", "page", name, "error", err)
		http.Error(w, "This page is temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// StaticHandler serves the stylesheet, script and sample photographs. An hour
// is long enough to be worth caching and short enough that replacing a file
// under its own name is not invisible for a day.
func StaticHandler(fsys fs.FS) (http.Handler, error) {
	sub, err := fs.Sub(fsys, "static")
	if err != nil {
		return nil, err
	}
	files := http.StripPrefix("/static", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, req)
	}), nil
}

// MediaHandler serves uploaded photographs from disk. Directory listings are
// refused so the uploads folder cannot be browsed.
func MediaHandler(dir string) http.Handler {
	files := http.StripPrefix("/media", http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/") {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, req)
	})
}

var hexColour = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

var templateFuncs = template.FuncMap{
	// money formats minor units with the shop's currency sign.
	"money": FormatMoney,

	// excerpt shortens a description for a lookbook card.
	"excerpt": Excerpt,

	// paragraphs splits a description on blank lines so the template can emit
	// real <p> elements instead of trusting the owner's text as markup.
	"paragraphs": func(s string) []string {
		var out []string
		for _, block := range strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n"), "\n\n") {
			if block = strings.TrimSpace(block); block != "" {
				out = append(out, block)
			}
		}
		return out
	},

	// tint validates a stored average colour before it reaches a stylesheet.
	"tint": func(v string) template.CSS {
		if hexColour.MatchString(v) {
			return template.CSS(v)
		}
		return template.CSS("#e8e0d5")
	},

	// seq counts 1..n, for the quantity selector.
	"seq": func(n int) []int {
		if n < 1 {
			return nil
		}
		out := make([]int, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, i)
		}
		return out
	},

	// add counts photographs from one instead of zero.
	"add": func(a, b int) int { return a + b },

	// amount is a bare decimal price, for a machine-readable meta tag.
	"amount": func(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) },

	"join": strings.Join,

	// ago renders a timestamp the way a person would say it.
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%d min ago", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%d hr ago", int(d.Hours()))
		case d < 48*time.Hour:
			return "yesterday"
		case d < 30*24*time.Hour:
			return fmt.Sprintf("%d days ago", int(d.Hours()/24))
		default:
			return t.Format("2 Jan 2006")
		}
	},

	// dict lets a partial be handed more than one value, since a template can
	// only pass a single argument.
	"dict": func(pairs ...any) (map[string]any, error) {
		if len(pairs)%2 != 0 {
			return nil, errors.New("dict takes an even number of arguments")
		}
		out := make(map[string]any, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			key, ok := pairs[i].(string)
			if !ok {
				return nil, errors.New("dict keys must be strings")
			}
			out[key] = pairs[i+1]
		}
		return out, nil
	},

	// phone renders a WhatsApp number the way a person would read it aloud.
	"phone": DisplayNumber,

	// instagram and tiktok take either a handle or a pasted profile URL and
	// always produce one canonical link.
	"instagram": func(v string) string { return socialURL("https://instagram.com/", v) },
	"tiktok":    func(v string) string { return socialURL("https://tiktok.com/@", v) },

	"year": func() int { return time.Now().Year() },
}

// socialURL turns "@name", "name", or a pasted profile URL into one link.
func socialURL(base, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
		return v
	}
	return base + socialHandle(v)
}
