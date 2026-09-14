package main

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------- session

func (a *App) sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// currentSession returns the live session for a request, if there is one.
func (a *App) currentSession(r *http.Request) (token, csrf string, ok bool) {
	token = a.sessionToken(r)
	csrf, ok = a.sessions.Lookup(token)
	return token, csrf, ok
}

// adminView assembles the shared admin page data, draining any queued flash
// messages as it goes.
func (a *App) adminView(r *http.Request, title string) AdminView {
	token, csrf, signedIn := a.currentSession(r)
	av := AdminView{View: a.newView(r, title, ""), CSRF: csrf, SignedIn: signedIn}
	if signedIn {
		av.Flashes = a.sessions.TakeFlashes(token)
		av.Warnings = a.warnings()
	}
	return av
}

// warnings are the things standing between this shop and its first order.
func (a *App) warnings() []string {
	set := a.store.Settings()
	var out []string
	if !set.OrderingEnabled() {
		out = append(out, "No WhatsApp number is set, so every order button is switched off. Add your number under Settings.")
	}
	if set.BaseURL == "" {
		out = append(out, "No public web address is set. WhatsApp can only show the dress photograph in the message once it can reach your site — add the address under Settings after you put the shop online.")
	}
	if len(a.store.Dresses()) == 0 {
		out = append(out, "The lookbook is empty. Add your first dress below, or load the sample collection to see how a full page looks.")
	}
	return out
}

// requireAdmin gates every admin route behind a live session.
func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if _, _, ok := a.currentSession(r); ok {
			next(w, r)
			return
		}
		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/admin/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		a.showError(w, r, http.StatusForbidden, "Your session has ended",
			"Please sign in again, then repeat that change.")
	}
}

// postOK verifies that a form submission came from this site and carries the
// session's own CSRF token. The form must already have been parsed.
func (a *App) postOK(w http.ResponseWriter, r *http.Request) bool {
	if !sameOriginPost(r) {
		a.showError(w, r, http.StatusForbidden, "That request was blocked",
			"The form did not come from this site.")
		return false
	}
	_, csrf, ok := a.currentSession(r)
	if !ok {
		a.showError(w, r, http.StatusForbidden, "Your session has ended",
			"Please sign in again, then repeat that change.")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(csrf)) != 1 {
		slog.Warn("csrf token mismatch", "path", r.URL.Path)
		a.showError(w, r, http.StatusForbidden, "That request was blocked",
			"The form's security token did not match. Reload the page and try once more.")
		return false
	}
	return true
}

// sameOriginPost trusts a browser's own account of where a request came from,
// preferring Sec-Fetch-Site and falling back to Origin for older clients.
func sameOriginPost(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "same-origin", "none":
		return true
	case "":
		// No Fetch Metadata; fall through to the Origin header.
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// ---------------------------------------------------------------- sign in

type adminLoginView struct {
	AdminView
	Error string
	Next  string
}

func (a *App) handleAdminRoot(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if _, _, ok := a.currentSession(r); ok {
		http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if _, _, ok := a.currentSession(r); ok {
		http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
		return
	}
	a.view.Render(w, http.StatusOK, "admin_login.html", adminLoginView{
		AdminView: a.adminView(r, "Sign in"),
		Next:      safeNext(r.URL.Query().Get("next")),
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		a.showError(w, r, http.StatusBadRequest, "That form could not be read", "Please try again.")
		return
	}
	if !sameOriginPost(r) {
		a.showError(w, r, http.StatusForbidden, "That request was blocked",
			"The sign-in form did not come from this site.")
		return
	}

	next := safeNext(r.PostFormValue("next"))
	client := clientKey(r, a.trustProxy)

	if wait, blocked := a.throttle.Blocked(client); blocked {
		a.view.Render(w, http.StatusTooManyRequests, "admin_login.html", adminLoginView{
			AdminView: a.adminView(r, "Sign in"),
			Next:      next,
			Error:     fmt.Sprintf("Too many attempts. Try again in %s.", wait),
		})
		return
	}

	if !a.credential.Verify(r.PostFormValue("password")) {
		a.throttle.Failed(client)
		slog.Warn("admin sign-in refused", "client", client)
		a.view.Render(w, http.StatusUnauthorized, "admin_login.html", adminLoginView{
			AdminView: a.adminView(r, "Sign in"),
			Next:      next,
			Error:     "That password is not right.",
		})
		return
	}

	a.throttle.Passed(client)
	token, _ := a.sessions.Start()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     sessionPath,
		HttpOnly: true,
		Secure:   isSecureRequest(r, a.trustProxy),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
	slog.Info("admin signed in", "client", client)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if err := r.ParseForm(); err != nil {
		a.showError(w, r, http.StatusBadRequest, "That form could not be read", "Please try again.")
		return
	}
	if !a.postOK(w, r) {
		return
	}
	a.sessions.End(a.sessionToken(r))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     sessionPath,
		HttpOnly: true,
		Secure:   isSecureRequest(r, a.trustProxy),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// safeNext keeps the post-sign-in redirect inside the admin area, so the
// parameter cannot be used to bounce someone off the site.
func safeNext(candidate string) string {
	if strings.HasPrefix(candidate, "/admin") && !strings.HasPrefix(candidate, "/admin/login") &&
		!strings.Contains(candidate, "//") && !strings.Contains(candidate, "\\") {
		return candidate
	}
	return "/admin/dresses"
}
