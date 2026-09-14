package main

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type contextKey int

const nonceContextKey contextKey = iota

// contentSecurityPolicy is assembled per request because the nonce changes.
// Everything comes from this origin apart from the two Google Fonts hosts;
// wa.me is named in form-action because the order form's response is a
// redirect out to WhatsApp, and some browsers check the redirect target.
//
// An empty nonce leaves the nonce sources out altogether. A statically
// exported site is served by a CDN that sends one fixed header to every
// visitor, so it has no per-request value to name — and it does not need one,
// because the storefront's stylesheet and script are both external files that
// 'self' already covers. Building both policies here keeps them from drifting.
func contentSecurityPolicy(nonce string) string {
	style := "style-src 'self' https://fonts.googleapis.com"
	script := "script-src 'self'"
	if nonce != "" {
		style = "style-src 'self' 'nonce-" + nonce + "' https://fonts.googleapis.com"
		script = "script-src 'self' 'nonce-" + nonce + "'"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"img-src 'self' data:",
		"font-src 'self' https://fonts.gstatic.com",
		style,
		// A card carries its photograph's average colour as a style attribute,
		// so that a placeholder can be tinted before the image arrives. Only
		// style attributes are allowed inline — a nonce cannot label one, and
		// every value passes through the tint filter in render.go first.
		"style-src-attr 'unsafe-inline'",
		script,
		"connect-src 'self'",
		"form-action 'self' https://wa.me",
	}, "; ")
}

// WithSecurityHeaders mints a per-request nonce and sets the headers that
// keep the storefront boring for anyone poking at it.
func WithSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := randomToken()[:22]
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy(nonce))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceContextKey, nonce)))
	})
}

// nonceFrom pulls the request's CSP nonce so a template can label its
// dynamic <style> block.
func nonceFrom(r *http.Request) string {
	if v, ok := r.Context().Value(nonceContextKey).(string); ok {
		return v
	}
	return ""
}

// statusRecorder remembers what was actually sent, for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// WithLogging records one line per request. Asset traffic drops to debug so
// the log stays readable while the shop is being used.
func WithLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, mediaURLPrefix) {
			level = slog.LevelDebug
		}
		if rec.status >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"ms", time.Since(start).Milliseconds(),
		)
	})
}

// WithRecovery keeps one bad request from taking the shop offline.
func WithRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic recovered",
					"path", r.URL.Path, "value", v, "stack", string(debug.Stack()))
				http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// noStore marks a response as private: admin pages must not be left in a
// shared cache or restored from the back-forward cache after signing out.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
}
