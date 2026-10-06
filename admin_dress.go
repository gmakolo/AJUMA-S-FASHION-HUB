package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
)

// maxFormBytes caps a whole dress submission: five photographs plus the text.
const maxFormBytes = maxImagesPerDress*maxUploadBytes + (1 << 20)

// ---------------------------------------------------------------- dashboard

type adminIndexView struct {
	AdminView
	Dresses      []Dress
	LiveCount    int
	SoldOutCount int
	NoPhotoCount int
	CanSeed      bool
}

func (a *App) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	all := a.store.Dresses()
	v := adminIndexView{
		AdminView: a.adminView(r, "Lookbook"),
		Dresses:   all,
		CanSeed:   len(all) == 0,
	}
	for _, d := range all {
		if d.SoldOut {
			v.SoldOutCount++
		} else {
			v.LiveCount++
		}
		if !d.HasImage() {
			v.NoPhotoCount++
		}
	}
	a.view.Render(w, http.StatusOK, "admin_index.html", v)
}

// ---------------------------------------------------------------- the form

type adminFormView struct {
	AdminView
	Dress     Dress
	IsNew     bool
	Errors    []string
	PriceText string
	SizesText string
	MaxImages int
}

func (a *App) handleDressNewForm(w http.ResponseWriter, r *http.Request) {
	a.renderDressForm(w, r, http.StatusOK,
		Dress{Sizes: []string{"S", "M", "L", "XL"}}, "", true, nil)
}

func (a *App) handleDressEditForm(w http.ResponseWriter, r *http.Request) {
	d, ok := a.store.ByID(r.PathValue("id"))
	if !ok {
		a.showError(w, r, http.StatusNotFound, "That dress is not in the lookbook",
			"It may already have been removed.")
		return
	}
	a.renderDressForm(w, r, http.StatusOK, d, priceInput(d.PriceMinor), false, nil)
}

// renderDressForm draws the form, whether it is blank, being edited, or being
// handed back with everything the owner typed still in place.
func (a *App) renderDressForm(w http.ResponseWriter, r *http.Request, status int,
	d Dress, priceText string, isNew bool, errs []string) {

	title := "Edit " + d.Name
	if isNew {
		title = "Add a dress"
	}
	if priceText == "" && d.PriceMinor > 0 {
		priceText = priceInput(d.PriceMinor)
	}
	a.view.Render(w, status, "admin_form.html", adminFormView{
		AdminView: a.adminView(r, title),
		Dress:     d,
		IsNew:     isNew,
		Errors:    errs,
		PriceText: priceText,
		SizesText: strings.Join(d.Sizes, ", "),
		MaxImages: maxImagesPerDress,
	})
}

// readDressForm folds the posted fields into a dress, collecting every
// problem rather than stopping at the first one.
func readDressForm(r *http.Request, d Dress) (Dress, string, []string) {
	var errs []string

	d.Name = trimTo(oneLine(r.PostFormValue("name")), 90)
	if d.Name == "" {
		errs = append(errs, "Give the dress a name.")
	}
	d.Description = trimTo(stripControl(r.PostFormValue("description")), 1500)
	if d.Description == "" {
		errs = append(errs, "Write a short description — it is what a buyer reads on the front page.")
	}
	d.Fabric = trimTo(oneLine(r.PostFormValue("fabric")), 90)
	d.Category = trimTo(oneLine(r.PostFormValue("category")), 40)
	d.Sizes = ParseList(r.PostFormValue("sizes"))
	d.Featured = r.PostFormValue("featured") != ""
	d.SoldOut = r.PostFormValue("sold_out") != ""

	priceText := strings.TrimSpace(r.PostFormValue("price"))
	if priceText != "" {
		price, err := ParsePrice(priceText)
		if err != nil {
			errs = append(errs, sentence(err.Error()))
		} else {
			d.PriceMinor = price
		}
	}
	return d, priceText, errs
}

// attachUploads saves every photograph on the form against the dress.
func (a *App) attachUploads(r *http.Request, d Dress) (Dress, []string) {
	if r.MultipartForm == nil {
		return d, nil
	}
	var errs []string
	alt := d.Name
	if d.Fabric != "" {
		alt = d.Name + ", " + d.Fabric
	}
	for _, header := range r.MultipartForm.File["photos"] {
		if header.Size == 0 {
			continue
		}
		if len(d.Images) >= maxImagesPerDress {
			errs = append(errs, fmt.Sprintf(
				"Only %d photographs fit on one dress, so the rest were left out.", maxImagesPerDress))
			break
		}
		img, err := a.media.Save(header, alt)
		if err != nil {
			errs = append(errs, sentence(err.Error()))
			continue
		}
		d.Images = append(d.Images, img)
	}
	return d, errs
}

// parseDressPost reads a multipart submission and checks its CSRF token.
func (a *App) parseDressPost(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		a.showError(w, r, http.StatusRequestEntityTooLarge, "That upload was too large",
			fmt.Sprintf("Send at most %d photographs of 8 MB each.", maxImagesPerDress))
		return false
	}
	return a.postOK(w, r)
}

func (a *App) handleDressCreate(w http.ResponseWriter, r *http.Request) {
	if !a.parseDressPost(w, r) {
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	// The text is validated before a single file is written, so a rejected
	// form never leaves orphaned photographs in the uploads directory.
	d, priceText, errs := readDressForm(r, Dress{})
	if len(errs) > 0 {
		a.renderDressForm(w, r, http.StatusUnprocessableEntity, d, priceText, true, errs)
		return
	}

	d, uploadErrs := a.attachUploads(r, d)
	created, err := a.store.Create(d)
	if err != nil {
		slog.Error("create dress", "error", err)
		a.media.RemoveAll(d.Images)
		a.renderDressForm(w, r, http.StatusInternalServerError, d, priceText, true,
			[]string{"The lookbook could not be saved. Please try again."})
		return
	}

	token := a.sessionToken(r)
	for _, e := range uploadErrs {
		a.sessions.Flash(token, "warn", e)
	}
	if !created.HasImage() {
		a.sessions.Flash(token, "warn",
			created.Name+" has no photograph yet, so it shows as a plain card.")
	}
	a.sessions.Flash(token, "ok",
		fmt.Sprintf("%s (%s) is live on the front page.", created.Name, created.Ref))
	http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
}

func (a *App) handleDressUpdate(w http.ResponseWriter, r *http.Request) {
	stored, ok := a.store.ByID(r.PathValue("id"))
	if !ok {
		a.showError(w, r, http.StatusNotFound, "That dress is not in the lookbook",
			"It may already have been removed.")
		return
	}
	if !a.parseDressPost(w, r) {
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	d, priceText, errs := readDressForm(r, stored)
	if len(errs) > 0 {
		a.renderDressForm(w, r, http.StatusUnprocessableEntity, d, priceText, false, errs)
		return
	}

	d, uploadErrs := a.attachUploads(r, d)
	saved, err := a.store.Update(d)
	if err != nil {
		slog.Error("update dress", "id", d.ID, "error", err)
		a.renderDressForm(w, r, http.StatusInternalServerError, d, priceText, false,
			[]string{"The change could not be saved. Please try again."})
		return
	}

	token := a.sessionToken(r)
	for _, e := range uploadErrs {
		a.sessions.Flash(token, "warn", e)
	}
	a.sessions.Flash(token, "ok", saved.Name+" has been updated.")
	http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
}

// simplePost parses a small form and checks its token, for the buttons that
// carry nothing but an id.
func (a *App) simplePost(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		a.showError(w, r, http.StatusBadRequest, "That form could not be read", "Please try again.")
		return false
	}
	return a.postOK(w, r)
}

func (a *App) handleDressDelete(w http.ResponseWriter, r *http.Request) {
	if !a.simplePost(w, r) {
		return
	}
	removed, err := a.store.Delete(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			a.showError(w, r, http.StatusNotFound, "That dress is not in the lookbook",
				"It may already have been removed.")
			return
		}
		slog.Error("delete dress", "error", err)
		a.showError(w, r, http.StatusInternalServerError, "That dress could not be removed",
			"Please try again.")
		return
	}
	a.media.RemoveAll(removed.Images)
	a.sessions.Flash(a.sessionToken(r), "ok", removed.Name+" has been taken down.")
	http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
}

func (a *App) handleDressMove(w http.ResponseWriter, r *http.Request) {
	if !a.simplePost(w, r) {
		return
	}
	delta := 1
	if r.PostFormValue("dir") == "up" {
		delta = -1
	}
	if err := a.store.Move(r.PathValue("id"), delta); err != nil && !errors.Is(err, ErrNotFound) {
		slog.Error("reorder lookbook", "error", err)
	}
	http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
}

// handleDressPhoto removes one photograph, or promotes it to the cover.
func (a *App) handleDressPhoto(w http.ResponseWriter, r *http.Request) {
	if !a.simplePost(w, r) {
		return
	}
	d, ok := a.store.ByID(r.PathValue("id"))
	if !ok {
		a.showError(w, r, http.StatusNotFound, "That dress is not in the lookbook",
			"It may already have been removed.")
		return
	}

	src := r.PostFormValue("src")
	index := -1
	for i, img := range d.Images {
		if img.Src == src {
			index = i
			break
		}
	}
	if index < 0 {
		http.Redirect(w, r, "/admin/dresses/"+d.ID, http.StatusSeeOther)
		return
	}

	token := a.sessionToken(r)
	switch r.PostFormValue("action") {
	case "remove":
		removed := d.Images[index]
		d.Images = append(d.Images[:index:index], d.Images[index+1:]...)
		if _, err := a.store.Update(d); err != nil {
			slog.Error("remove photograph", "error", err)
			a.sessions.Flash(token, "error", "That photograph could not be removed.")
			break
		}
		_ = a.media.Remove(removed.Src)
		a.sessions.Flash(token, "ok", "Photograph removed.")
	case "primary":
		promoted := d.Images[index]
		rest := append(d.Images[:index:index], d.Images[index+1:]...)
		d.Images = append([]Image{promoted}, rest...)
		if _, err := a.store.Update(d); err != nil {
			slog.Error("promote photograph", "error", err)
			a.sessions.Flash(token, "error", "That photograph could not be moved.")
			break
		}
		a.sessions.Flash(token, "ok", "Cover photograph changed.")
	}
	http.Redirect(w, r, "/admin/dresses/"+d.ID, http.StatusSeeOther)
}

// priceInput renders minor units back into something to type over: 2850000
// becomes "28500", and 2850050 becomes "28500.50".
func priceInput(minor int64) string {
	if minor <= 0 {
		return ""
	}
	if minor%100 == 0 {
		return fmt.Sprintf("%d", minor/100)
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

// sentence makes a lower-case error message read like one.
func sentence(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	out := string(runes)
	if !strings.ContainsAny(out[len(out)-1:], ".!?") {
		out += "."
	}
	return out
}
