package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const passwordResetTTL = 30 * time.Minute

type memberAuthView struct {
	View
	Error, Message, Name, Email, Next string
}
type passwordRecoveryView struct {
	View
	Error, Message string
}
type memberDashboardView struct {
	View
	Designs []Dress
}
type memberDesignView struct {
	View
	CSRF                 string
	Dress                Dress
	PriceText, SizesText string
	Errors               []string
	MaxImages            int
}

func (a *App) requireMember(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.static {
			next(w, r)
			return
		}
		noStore(w)
		if a.memberFor(r).ID == "" {
			http.Redirect(w, r, "/login?next="+safeMemberNext(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func safeMemberNext(s string) string {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "\\\r\n") {
		return s
	}
	return "/"
}

func (a *App) handleMemberLoginForm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if a.memberFor(r).ID != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	message := ""
	if r.URL.Query().Get("reset") == "success" {
		message = "Your password has been changed. Sign in with your new password."
	}
	a.view.Render(w, http.StatusOK, "login.html", memberAuthView{View: a.newView(r, "Sign in", "Sign in to explore AJ FASHION AND DESIGN."), Next: safeMemberNext(r.URL.Query().Get("next")), Message: message})
}
func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if a.memberFor(r).ID != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.view.Render(w, http.StatusOK, "register.html", memberAuthView{View: a.newView(r, "Create account", "Join AJ FASHION AND DESIGN to share your fashion designs.")})
}

func (a *App) handleForgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	a.view.Render(w, http.StatusOK, "forgot_password.html", passwordRecoveryView{View: a.newView(r, "Forgot password", "Request a password reset code.")})
}

func (a *App) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	client := clientKey(r, a.trustProxy)
	if wait, blocked := a.resetLimit.Blocked(client); blocked {
		a.renderRecovery(w, r, http.StatusTooManyRequests, passwordRecoveryView{Error: fmt.Sprintf("Please wait %s before trying again.", wait)})
		return
	}
	a.resetLimit.Failed(client)
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	if a.mailer != nil {
		if member, ok := a.members.ByEmail(email); ok {
			value, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
			if err != nil {
				slog.Error("generate password recovery code", "error", err)
				a.renderRecovery(w, r, http.StatusOK, passwordRecoveryView{Message: "If an account uses that email address, we’ll send a recovery code shortly."})
				return
			}
			code := fmt.Sprintf("%08d", value.Int64())
			sum := sha256.Sum256([]byte(email + "\x00" + code))
			if err := a.members.SetReset(email, hex.EncodeToString(sum[:]), time.Now().Add(passwordResetTTL)); err != nil {
				slog.Error("save password reset", "error", err)
			} else {
				go func() {
					if err := a.mailer.SendPasswordReset(member.Email, code); err != nil {
						slog.Error("send password reset email", "error", err)
					}
				}()
			}
		}
	} else {
		slog.Warn("password recovery requested but SMTP is not configured")
	}
	a.renderRecovery(w, r, http.StatusOK, passwordRecoveryView{Message: "If an account uses that email address, we’ll send a recovery code shortly."})
}

func (a *App) handleResetPasswordForm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	a.view.Render(w, http.StatusOK, "reset_password.html", passwordRecoveryView{View: a.newView(r, "Set a new password", "Choose a new password for your account.")})
}

func (a *App) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	client := clientKey(r, a.trustProxy)
	if wait, blocked := a.verifyLimit.Blocked(client); blocked {
		a.renderRecovery(w, r, http.StatusTooManyRequests, passwordRecoveryView{Error: fmt.Sprintf("Too many code attempts. Try again in %s.", wait)})
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	code := strings.TrimSpace(r.PostFormValue("code"))
	sum := sha256.Sum256([]byte(email + "\x00" + code))
	memberID, ok, err := a.members.ResetPassword(email, hex.EncodeToString(sum[:]), r.PostFormValue("password"))
	if err != nil {
		a.renderRecovery(w, r, http.StatusUnprocessableEntity, passwordRecoveryView{Error: err.Error()})
		return
	}
	if !ok {
		a.verifyLimit.Failed(client)
		a.renderRecovery(w, r, http.StatusUnprocessableEntity, passwordRecoveryView{Error: "That email and recovery code are invalid or expired. Request a new code."})
		return
	}
	a.verifyLimit.Passed(client)
	a.sessions.EndMember(memberID)
	http.Redirect(w, r, "/login?reset=success", http.StatusSeeOther)
}

func (a *App) renderRecovery(w http.ResponseWriter, r *http.Request, status int, v passwordRecoveryView) {
	if v.View.Settings.BrandName == "" {
		v.View = a.newView(r, "Password recovery", "Recover your account password.")
	}
	page := "forgot_password.html"
	if r.URL.Path == "/reset-password" {
		page = "reset_password.html"
	}
	a.view.Render(w, status, page, v)
}
func (a *App) renderAuthError(w http.ResponseWriter, r *http.Request, status int, register bool, v memberAuthView) {
	page := "login.html"
	if register {
		page = "register.html"
	}
	a.view.Render(w, status, page, v)
}
func (a *App) handleMemberLogin(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	client := clientKey(r, a.trustProxy)
	if wait, blocked := a.throttle.Blocked(client); blocked {
		a.renderAuthError(w, r, http.StatusTooManyRequests, false, memberAuthView{View: a.newView(r, "Sign in", ""), Error: fmt.Sprintf("Too many attempts. Try again in %s.", wait), Email: r.PostFormValue("email"), Next: safeMemberNext(r.PostFormValue("next"))})
		return
	}
	m, ok := a.members.SignIn(r.PostFormValue("email"), r.PostFormValue("password"))
	if !ok {
		a.throttle.Failed(client)
		a.renderAuthError(w, r, http.StatusUnauthorized, false, memberAuthView{View: a.newView(r, "Sign in", ""), Error: "Email or password was not recognized.", Email: r.PostFormValue("email"), Next: safeMemberNext(r.PostFormValue("next"))})
		return
	}
	a.throttle.Passed(client)
	a.startMemberSession(w, r, m)
	http.Redirect(w, r, safeMemberNext(r.PostFormValue("next")), http.StatusSeeOther)
}
func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	r.Body = http.MaxBytesReader(w, r.Body, 12<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	name, email := r.PostFormValue("name"), r.PostFormValue("email")
	m, err := a.members.Register(name, email, r.PostFormValue("password"))
	if err != nil {
		a.renderAuthError(w, r, http.StatusUnprocessableEntity, true, memberAuthView{View: a.newView(r, "Create account", ""), Error: err.Error(), Name: name, Email: email})
		return
	}
	a.startMemberSession(w, r, m)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *App) startMemberSession(w http.ResponseWriter, r *http.Request, m Member) {
	token, _ := a.sessions.StartMember(m.ID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: isSecureRequest(r, a.trustProxy), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL / time.Second)})
}
func (a *App) handleMemberLogout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if err := r.ParseForm(); err != nil || !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	_, csrf, ok := a.currentSession(r)
	if !ok || r.PostFormValue("csrf") != csrf {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	a.sessions.End(a.sessionToken(r))
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: isSecureRequest(r, a.trustProxy), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *App) handleMemberDashboard(w http.ResponseWriter, r *http.Request) {
	m := a.memberFor(r)
	a.view.Render(w, http.StatusOK, "dashboard.html", memberDashboardView{View: a.newView(r, "My designs", "Your member profile and posted designs."), Designs: a.store.DressesByOwner(m.ID)})
}

func (a *App) handleMemberDesignDelete(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil || !sameOriginPost(r) {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	_, csrf, ok := a.currentSession(r)
	if !ok || r.PostFormValue("csrf") != csrf {
		http.Error(w, "Request blocked", http.StatusForbidden)
		return
	}
	member := a.memberFor(r)
	removed, err := a.store.DeleteOwned(r.PathValue("id"), member.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			a.showError(w, r, http.StatusNotFound, "Design not found", "That design may already be removed or may not belong to your account.")
			return
		}
		slog.Error("delete member design", "error", err)
		a.showError(w, r, http.StatusInternalServerError, "Design could not be deleted", "Please try again.")
		return
	}
	a.media.RemoveAll(removed.Images)
	a.sessions.Flash(a.sessionToken(r), "ok", "Your design has been deleted.")
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *App) handleNewDesignForm(w http.ResponseWriter, r *http.Request) {
	_, csrf, _ := a.currentSession(r)
	a.renderMemberDesign(w, r, http.StatusOK, Dress{Sizes: []string{"S", "M", "L", "XL"}}, "", nil, csrf)
}
func (a *App) renderMemberDesign(w http.ResponseWriter, r *http.Request, status int, d Dress, price string, errs []string, csrf string) {
	a.view.Render(w, status, "design_new.html", memberDesignView{View: a.newView(r, "Share a design", "Post your sewn outfit to AJ FASHION AND DESIGN."), CSRF: csrf, Dress: d, PriceText: price, SizesText: strings.Join(d.Sizes, ", "), Errors: errs, MaxImages: maxImagesPerDress})
}
func (a *App) handleCreateDesign(w http.ResponseWriter, r *http.Request) {
	if !a.parseDressPost(w, r) {
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	m := a.memberFor(r)
	d, price, errs := readDressForm(r, Dress{})
	if r.MultipartForm == nil || len(r.MultipartForm.File["photos"]) == 0 {
		errs = append(errs, "Choose at least one outfit photo to upload.")
	}
	if len(errs) > 0 {
		_, csrf, _ := a.currentSession(r)
		a.renderMemberDesign(w, r, http.StatusUnprocessableEntity, d, price, errs, csrf)
		return
	}
	d.OwnerID = m.ID
	d.OwnerName = m.Name
	d, dErrors := a.attachUploads(r, d)
	if !d.HasImage() {
		if len(dErrors) == 0 {
			dErrors = append(dErrors, "The photo could not be read. Choose a JPG, PNG, GIF or WebP image.")
		}
		_, csrf, _ := a.currentSession(r)
		a.renderMemberDesign(w, r, http.StatusUnprocessableEntity, d, price, dErrors, csrf)
		return
	}
	created, err := a.store.Create(d)
	if err != nil {
		a.renderMemberDesign(w, r, http.StatusInternalServerError, d, price, []string{"Your design could not be saved. Please try again."}, "")
		return
	}
	if len(dErrors) > 0 {
		a.sessions.Flash(a.sessionToken(r), "warn", strings.Join(dErrors, " "))
	}
	a.sessions.Flash(a.sessionToken(r), "ok", fmt.Sprintf("%s is now in the gallery.", created.Name))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
