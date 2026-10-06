// AJ FASHION AND DESIGN — a member gallery where tailors share outfits and designs.
//
// The whole thing is the Go standard library: net/http for routing, io/fs and
// embed to carry the templates and stylesheet inside the binary, html/template
// for rendering, image/jpeg for the photographs, and a JSON document for
// storage. There is nothing to install and nothing to configure to see it run:
//
//	go run .
//
// Members create accounts on the site. Accounts, designs and photographs live
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
		verbose = flag.Bool("v", false, "log every request, including assets")

		export = flag.String("export", "",
			"write the storefront to this folder as flat files and exit, for a static host")
		base = flag.String("base", "",
			"the address the exported shop will be served from, e.g. https://ajuma.netlify.app")
	)
	flag.Parse()

	if *export != "" {
		return errors.New("member sign-in requires the Go server; static export cannot protect the gallery")
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

	members, err := OpenMembers(*dataDir)
	if err != nil {
		return err
	}
	mailer, err := SMTPMailerFromEnv()
	if err != nil {
		return err
	}
	if mailer == nil {
		slog.Warn("password recovery email is not configured; set SMTP environment variables")
	}

	app := &App{
		store:       store,
		members:     members,
		mailer:      mailer,
		media:       media,
		view:        view,
		sessions:    NewSessions(),
		throttle:    NewThrottle(),
		resetLimit:  NewThrottle(),
		verifyLimit: NewThrottle(),
		trustProxy:  *trustProxy,
		static:      exporting,
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

	slog.Info("AJ FASHION AND DESIGN is up",
		"addr", *addr, "shop", store.Settings().BrandName, "data", *dataDir, "dev", *dev)
	fmt.Fprintf(os.Stderr, "\n  AJ FASHION AND DESIGN   http://localhost%s/\n  sign in                 http://localhost%s/login\n\n",
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
	mux.HandleFunc("GET /{$}", a.requireMember(a.handleHome))
	mux.HandleFunc("GET /dress/{slug}", a.requireMember(a.handleDress))
	mux.HandleFunc("POST /dress/{slug}/react", a.requireMember(a.handleDressReaction))
	mux.HandleFunc("GET /dress/{slug}/order", a.requireMember(a.handleOrder))
	mux.HandleFunc("POST /dress/{slug}/order", a.requireMember(a.handleOrder))
	mux.HandleFunc("GET /login", a.handleMemberLoginForm)
	mux.HandleFunc("POST /login", a.handleMemberLogin)
	mux.HandleFunc("GET /register", a.handleRegisterForm)
	mux.HandleFunc("POST /register", a.handleRegister)
	mux.HandleFunc("GET /forgot-password", a.handleForgotPasswordForm)
	mux.HandleFunc("POST /forgot-password", a.handleForgotPassword)
	mux.HandleFunc("GET /reset-password", a.handleResetPasswordForm)
	mux.HandleFunc("POST /reset-password", a.handleResetPassword)
	mux.HandleFunc("POST /logout", a.handleMemberLogout)
	mux.HandleFunc("GET /dashboard", a.requireMember(a.handleMemberDashboard))
	mux.HandleFunc("GET /designs/new", a.requireMember(a.handleNewDesignForm))
	mux.HandleFunc("POST /designs/new", a.requireMember(a.handleCreateDesign))
	mux.HandleFunc("POST /designs/{id}/delete", a.requireMember(a.handleMemberDesignDelete))

	// Assets and machine-readable extras.
	mux.Handle("GET /static/", static)
	mux.Handle("GET /media/", MediaHandler(a.media.Dir()))
	mux.HandleFunc("GET /robots.txt", a.handleRobots)
	mux.HandleFunc("GET /sitemap.xml", a.handleSitemap)
	mux.HandleFunc("GET /healthz", a.handleHealth)

	// Anything else is not part of the member site.
	mux.HandleFunc("/", a.handleNotFound)
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
