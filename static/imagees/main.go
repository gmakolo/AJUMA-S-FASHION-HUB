// Ajuma Fashion Hub — a small shop that shows the dresses its owner makes and
// hands each order to WhatsApp, where she already talks to her customers.
//
// The whole thing is the Go standard library: net/http for routing, io/fs and
// embed to carry the templates and stylesheet inside the binary, html/template
// for rendering, image/jpeg for the photographs, and a JSON document for
// storage. There is nothing to install and nothing to configure to see it run:
//
//	go run .
//
// It prints an admin password on a first run and keeps everything it is given
// under ./data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ajuma:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr       = flag.String("addr", envOr("AJUMA_ADDR", ":8080"), "address to listen on")
		dataDir    = flag.String("data", envOr("AJUMA_DATA", "data"), "directory for the catalogue and uploaded photographs")
		dev        = flag.Bool("dev", false, "read templates and assets from disk on every request")
		trustProxy = flag.Bool("trust-proxy", os.Getenv("AJUMA_TRUST_PROXY") == "1",
			"trust X-Forwarded-For and X-Forwarded-Proto (only behind a reverse proxy you control)")
		hash    = flag.String("hash", "", "print the hash of this password and exit")
		verbose = flag.Bool("v", false, "log every request, including assets")

		export = flag.String("export", "",
			"write the storefront to this folder as flat files and exit, for a static host")
		base = flag.String("base", "",
			"the address the exported shop will be served from, e.g. https://ajuma.netlify.app")
	)
	flag.Parse()

	if *hash != "" {
		credential, err := NewCredential(*hash)
		if err != nil {
			return err
		}
		fmt.Println(credential)
		return nil
	}

	level := slog.LevelInfo
	if *verbose || *dev {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	store, err := OpenStore(*dataDir)
	if err != nil {
		return err
	}
	media, err := OpenMediaStore(filepath.Join(*dataDir, "media"))
	if err != nil {
		return err
	}

	assets := fs.FS(embedded)
	if *dev {
		assets = os.DirFS(".")
	}
	view, err := NewRenderer(assets, *dev)
	if err != nil {
		return err
	}
	static, err := StaticHandler(assets)
	if err != nil {
		return err
	}

	// An export renders pages and exits. It never reaches an admin route, so it
	// neither needs a password nor should invent and print one.
	exporting := *export != ""

	credential := Credential{}
	if !exporting {
		credential, err = adminCredential()
		if err != nil {
			return err
		}
	}

	app := &App{
		store:      store,
		media:      media,
		view:       view,
		sessions:   NewSessions(),
		throttle:   NewThrottle(),
		credential: credential,
		trustProxy: *trustProxy,
		static:     exporting,
	}

	mux := http.NewServeMux()
	app.routes(mux, static)

	if exporting {
		if *base != "" {
			store.OverrideBaseURL(*base)
		}
		fmt.Printf("\n  exporting %s to %s\n\n", store.Settings().BrandName, *export)
		// The routes alone, with none of the middleware: an export throws the
		// response headers away, and the headers a static host needs are written
		// once into _headers instead.
		return exportSite(app, mux, assets, *export)
	}

	handler := WithRecovery(WithLogging(WithSecurityHeaders(mux)))
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // generous: a photograph may be uploaded over a slow line
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	slog.Info("ajuma is up",
		"addr", *addr, "shop", store.Settings().BrandName, "data", *dataDir, "dev", *dev)
	fmt.Fprintf(os.Stderr, "\n  storefront  http://localhost%s/\n  admin       http://localhost%s/admin\n\n",
		portOf(*addr), portOf(*addr))

	return serve(server)
}

// serve runs the server until an interrupt, then lets requests in flight finish.
func serve(server *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

// routes is the whole URL surface of the shop in one readable block.
func (a *App) routes(mux *http.ServeMux, static http.Handler) {
	// Storefront.
	mux.HandleFunc("GET /{$}", a.handleHome)
	mux.HandleFunc("GET /dress/{slug}", a.handleDress)
	mux.HandleFunc("GET /dress/{slug}/order", a.handleOrder)
	mux.HandleFunc("POST /dress/{slug}/order", a.handleOrder)

	// Assets and machine-readable extras.
	mux.Handle("GET /static/", static)
	mux.Handle("GET /media/", MediaHandler(a.media.Dir()))
	mux.HandleFunc("GET /robots.txt", a.handleRobots)
	mux.HandleFunc("GET /sitemap.xml", a.handleSitemap)
	mux.HandleFunc("GET /healthz", a.handleHealth)

	// Signing in and out.
	mux.HandleFunc("GET /admin/login", a.handleLoginForm)
	mux.HandleFunc("POST /admin/login", a.handleLogin)
	mux.HandleFunc("POST /admin/logout", a.handleLogout)

	// The owner's side of the shop.
	mux.HandleFunc("GET /admin/dresses", a.requireAdmin(a.handleAdminIndex))
	mux.HandleFunc("GET /admin/dresses/{$}", a.requireAdmin(a.handleAdminIndex))
	mux.HandleFunc("GET /admin/dresses/new", a.requireAdmin(a.handleDressNewForm))
	mux.HandleFunc("POST /admin/dresses/new", a.requireAdmin(a.handleDressCreate))
	mux.HandleFunc("GET /admin/dresses/{id}", a.requireAdmin(a.handleDressEditForm))
	mux.HandleFunc("POST /admin/dresses/{id}", a.requireAdmin(a.handleDressUpdate))
	mux.HandleFunc("POST /admin/dresses/{id}/delete", a.requireAdmin(a.handleDressDelete))
	mux.HandleFunc("POST /admin/dresses/{id}/move", a.requireAdmin(a.handleDressMove))
	mux.HandleFunc("POST /admin/dresses/{id}/photo", a.requireAdmin(a.handleDressPhoto))
	mux.HandleFunc("GET /admin/settings", a.requireAdmin(a.handleSettingsForm))
	mux.HandleFunc("POST /admin/settings", a.requireAdmin(a.handleSettingsSave))
	mux.HandleFunc("POST /admin/sample", a.requireAdmin(a.handleSeedSamples))

	// Anything else, including a bare /admin.
	mux.HandleFunc("GET /admin/{$}", a.handleAdminRoot)
	mux.HandleFunc("GET /admin", a.handleAdminRoot)
	mux.HandleFunc("/", a.handleNotFound)
}

// adminCredential settles on a password verifier. A deployed instance carries
// only the hash; a first local run invents a password and prints it once, so
// there is never a default password to forget about.
func adminCredential() (Credential, error) {
	if encoded := strings.TrimSpace(os.Getenv("AJUMA_ADMIN_PASSWORD_HASH")); encoded != "" {
		credential, err := ParseCredential(encoded)
		if err != nil {
			return Credential{}, fmt.Errorf("AJUMA_ADMIN_PASSWORD_HASH: %w", err)
		}
		return credential, nil
	}

	if password := os.Getenv("AJUMA_ADMIN_PASSWORD"); password != "" {
		credential, err := NewCredential(password)
		if err != nil {
			return Credential{}, fmt.Errorf("AJUMA_ADMIN_PASSWORD: %w", err)
		}
		slog.Info("admin password taken from AJUMA_ADMIN_PASSWORD")
		return credential, nil
	}

	password := GeneratePassword()
	credential, err := NewCredential(password)
	if err != nil {
		return Credential{}, err
	}
	fmt.Fprintf(os.Stderr, `
  ┌─ Ajuma Fashion Hub ─────────────────────────────────────────┐
  │  No admin password was set, so here is one for this run:     │
  │                                                              │
  │      %-56s│
  │                                                              │
  │  It changes on every restart. To keep one password, run      │
  │  'go run . -hash "your password"' and put the line it        │
  │  prints in AJUMA_ADMIN_PASSWORD_HASH.                        │
  └──────────────────────────────────────────────────────────────┘

`, password)
	return credential, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// portOf turns a listen address into something clickable in a terminal.
func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":" + addr
}
