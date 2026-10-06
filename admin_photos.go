package main

import (
	"fmt"
	"log/slog"
	"net/http"
)

type adminPhotosView struct {
	AdminView
	Dresses  []Dress
	Selected string
	Errors   []string
}

func (a *App) handlePhotoUploadForm(w http.ResponseWriter, r *http.Request) {
	a.renderPhotoUpload(w, r, http.StatusOK, r.URL.Query().Get("dress"), nil)
}

func (a *App) renderPhotoUpload(w http.ResponseWriter, r *http.Request, status int, selected string, errs []string) {
	a.view.Render(w, status, "admin_photos.html", adminPhotosView{
		AdminView: a.adminView(r, "Upload photographs"),
		Dresses:   a.store.Dresses(),
		Selected:  selected,
		Errors:    errs,
	})
}

func (a *App) handlePhotoUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		a.showError(w, r, http.StatusRequestEntityTooLarge, "That upload was too large",
			fmt.Sprintf("Send at most %d photographs of 8 MB each.", maxImagesPerDress))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !a.postOK(w, r) {
		return
	}

	selected := r.PostFormValue("dress")
	d, ok := a.store.ByID(selected)
	if !ok {
		a.renderPhotoUpload(w, r, http.StatusUnprocessableEntity, selected, []string{"Choose a dress from the lookbook."})
		return
	}
	files := r.MultipartForm.File["photos"]
	if len(files) == 0 {
		a.renderPhotoUpload(w, r, http.StatusUnprocessableEntity, selected, []string{"Choose at least one photograph to upload."})
		return
	}
	if len(d.Images)+len(files) > maxImagesPerDress {
		a.renderPhotoUpload(w, r, http.StatusUnprocessableEntity, selected,
			[]string{fmt.Sprintf("%s has room for %d more photograph(s).", d.Name, maxImagesPerDress-len(d.Images))})
		return
	}

	alt := d.Name
	if d.Fabric != "" {
		alt += ", " + d.Fabric
	}
	added := make([]Image, 0, len(files))
	for _, file := range files {
		img, err := a.media.Save(file, alt)
		if err != nil {
			a.media.RemoveAll(added)
			a.renderPhotoUpload(w, r, http.StatusUnprocessableEntity, selected, []string{sentence(err.Error())})
			return
		}
		added = append(added, img)
	}
	d.Images = append(d.Images, added...)
	saved, err := a.store.Update(d)
	if err != nil {
		a.media.RemoveAll(added)
		slog.Error("upload photographs", "id", d.ID, "error", err)
		a.renderPhotoUpload(w, r, http.StatusInternalServerError, selected, []string{"The photographs could not be saved. Please try again."})
		return
	}
	a.sessions.Flash(a.sessionToken(r), "ok", fmt.Sprintf("Added %d photograph(s) to %s.", len(added), saved.Name))
	http.Redirect(w, r, "/admin/dresses/"+saved.ID, http.StatusSeeOther)
}
