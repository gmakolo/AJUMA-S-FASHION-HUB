package main

// Exporting the shop to flat files.
//
// The storefront is already nothing but server-rendered HTML: every visitor
// route is a GET that reads the catalogue and writes a page. Nothing about it
// needs to happen while a visitor is waiting, so it need not happen on a server
// at all. This file asks the running application for each of its own pages and
// writes the answers to disk, giving a folder that any static host will serve —
// including the ones that will not run a Go process.
//
// The studio stays where it belongs: on the owner's own machine, next to the
// catalogue and the photographs. She edits, exports, and publishes.
//
// Three things a CDN cannot do for us, and what happens instead:
//
//   - Read a query string. "/?c=Wrapper" becomes the page "/c/wrapper/", and
//     View.CategoryURL points the chips at it.
//   - Accept the order form. The button becomes a plain link to wa.me carrying
//     the default message; with scripting on, ajuma.js rewrites that link as the
//     buyer picks a size. The size and quantity fields only appear when the
//     script is there to carry them, so nothing on the page ever promises to
//     send a choice it cannot send.
//   - Search. The box is left out of an exported page rather than left there
//     doing nothing.

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// exportMarker is dropped in the output folder so a second export knows the
// folder is its own to clear, and refuses to touch one that is not.
const exportMarker = ".ajuma-export"

// capture is the ResponseWriter the export renders into: the handlers believe
// they are answering a request and the bytes land in memory. It is a dozen
// lines rather than an import of net/http/httptest, which belongs in tests.
type capture struct {
	status int
	header http.Header
	body   []byte
	wrote  bool
}

func newCapture() *capture {
	return &capture{status: http.StatusOK, header: http.Header{}}
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(status int) {
	if !c.wrote {
		c.status, c.wrote = status, true
	}
}

func (c *capture) Write(p []byte) (int, error) {
	c.wrote = true
	c.body = append(c.body, p...)
	return len(p), nil
}

// page is one address and the file it is written to.
type page struct {
	url  string
	file string
	want int // the status the handler is expected to answer with
}

// exportSite writes the storefront to dir. handler answers the same routes the
// server does, so an exported page is the page a visitor would have been served;
// assets is the filesystem holding static/.
func exportSite(a *App, handler http.Handler, assets fs.FS, dir string) error {
	set := a.store.Settings()
	if set.BaseURL == "" {
		return fmt.Errorf(`no public address recorded

  An order is a WhatsApp message carrying an absolute link to the dress and to
  its photograph, which is what makes WhatsApp show the picture. An exported
  site has no request to infer that address from, so it has to be told:

      go run . -export %s -base https://your-site.netlify.app

  Or record it once in the studio, under Settings, and leave the flag off`, dir)
	}

	if err := prepareDir(dir); err != nil {
		return err
	}

	pages := []page{{url: "/", file: "index.html", want: http.StatusOK}}
	for _, d := range a.store.Dresses() {
		pages = append(pages, page{
			url:  d.Path(),
			file: "dress/" + d.Slug + "/index.html",
			want: http.StatusOK,
		})
	}
	for _, c := range a.store.Categories() {
		pages = append(pages, page{
			url:  "/?c=" + url.QueryEscape(c),
			file: "c/" + Slugify(c) + "/index.html",
			want: http.StatusOK,
		})
	}
	pages = append(pages,
		page{url: "/robots.txt", file: "robots.txt", want: http.StatusOK},
		page{url: "/sitemap.xml", file: "sitemap.xml", want: http.StatusOK},
		// Netlify and every host like it serve this for an address that matches
		// no file, so the shop's own error page turns up instead of the host's.
		page{url: "/404", file: "404.html", want: http.StatusNotFound},
	)

	var files int
	var written int64
	for _, p := range pages {
		body, err := fetch(handler, p)
		if err != nil {
			return err
		}
		n, err := writeFile(dir, p.file, body)
		if err != nil {
			return err
		}
		files, written = files+1, written+int64(n)
		fmt.Printf("  %-40s %7s\n", p.file, size(int64(n)))
	}

	for _, tree := range []struct {
		fsys fs.FS
		root string
		into string
	}{
		{assets, "static", "static"},
		{os.DirFS(a.media.Dir()), ".", "media"},
	} {
		n, b, err := copyTree(tree.fsys, tree.root, dir, tree.into)
		if err != nil {
			return err
		}
		files, written = files+n, written+b
		fmt.Printf("  %-40s %7s  (%d files)\n", tree.into+"/", size(b), n)
	}

	n, err := writeFile(dir, "_headers", []byte(netlifyHeaders()))
	if err != nil {
		return err
	}
	files, written = files+1, written+int64(n)
	fmt.Printf("  %-40s %7s\n", "_headers", size(int64(n)))

	fmt.Printf("\n  %d files, %s, in %s\n", files, size(written), dir)
	fmt.Printf("  the shop will be served from %s\n\n", set.BaseURL)
	return nil
}

// prepareDir makes an empty folder to write into. A folder this export made
// before is cleared and reused; anything else is left alone and reported,
// because "-export ." should not be able to delete a source tree.
func prepareDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("read %s: %w", dir, err)
	case len(entries) > 0:
		if _, err := os.Stat(filepath.Join(dir, exportMarker)); err != nil {
			return fmt.Errorf("refusing to write over %s: it is not empty and was not "+
				"made by this export (no %s in it) — name an empty folder instead",
				dir, exportMarker)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clear %s: %w", dir, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, exportMarker), []byte(
		"Written by ajuma -export. Everything here is regenerated; edit the shop\n"+
			"in the studio instead. This file is what lets the next export clear the\n"+
			"folder without having to guess whether it may.\n"), 0o644)
}

// fetch asks the application for one of its own pages.
func fetch(handler http.Handler, p page) ([]byte, error) {
	// The host never reaches the output: every absolute link is built from the
	// public address recorded in settings, which exportSite insists on.
	req, err := http.NewRequest(http.MethodGet, "http://export.invalid"+p.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", p.url, err)
	}
	rec := newCapture()
	handler.ServeHTTP(rec, req)
	if rec.status != p.want {
		return nil, fmt.Errorf("%s answered %d, expected %d", p.url, rec.status, p.want)
	}
	if len(rec.body) == 0 {
		return nil, fmt.Errorf("%s came back empty", p.url)
	}
	return rec.body, nil
}

// writeFile writes one file, making the folders above it as needed.
func writeFile(dir, name string, body []byte) (int, error) {
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, fmt.Errorf("create folder for %s: %w", name, err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		return 0, fmt.Errorf("write %s: %w", name, err)
	}
	return len(body), nil
}

// copyTree copies root out of fsys into dir/into, keeping the shape. A missing
// source is not an error: a shop whose photographs are all still the samples
// has no uploads folder yet.
func copyTree(fsys fs.FS, root, dir, into string) (files int, written int64, err error) {
	if _, err := fs.Stat(fsys, root); err != nil {
		return 0, 0, nil
	}
	err = fs.WalkDir(fsys, root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		n, err := writeFile(dir, into+"/"+rel, raw)
		if err != nil {
			return err
		}
		files, written = files+1, written+int64(n)
		return nil
	})
	return files, written, err
}

// netlifyHeaders is the _headers file. It carries over the headers the Go
// middleware would have set, since on a static host there is no middleware —
// only the CDN, and this is how it is told. Cache-Control is deliberately
// absent: Netlify already revalidates static files against an ETag and clears
// its edges on deploy, so a replaced photograph is visible immediately.
func netlifyHeaders() string {
	var b strings.Builder
	b.WriteString("# Written by ajuma -export.\n/*\n")
	headers := map[string]string{
		"Content-Security-Policy":    contentSecurityPolicy(""),
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=(), payment=()",
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %s: %s\n", name, headers[name])
	}
	return b.String()
}

// size reports a byte count the way a person would say it.
func size(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
