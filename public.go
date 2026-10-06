package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// homeView backs the storefront's front page.
type homeView struct {
	View
	Hero     Dress
	HasHero  bool
	Dresses  []Dress
	Total    int
	Category string
	Query    string
}

// Filtering reports whether the visitor has narrowed the lookbook.
func (h homeView) Filtering() bool { return h.Category != "" || h.Query != "" }

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	all := a.store.Dresses()
	if len(all) == 0 {
		all = sampleDresses
	}
	category := trimTo(oneLine(r.URL.Query().Get("c")), 40)
	query := trimTo(oneLine(r.URL.Query().Get("q")), 80)

	set := a.store.Settings()
	v := a.newView(r, "", "")
	v.Title = set.BrandName + " — " + Excerpt(set.Tagline, 70)
	hero, hasHero := heroDress(all)

	a.view.Render(w, http.StatusOK, "home.html", homeView{
		View:     v,
		Hero:     hero,
		HasHero:  hasHero,
		Dresses:  filterDresses(all, category, query),
		Total:    len(all),
		Category: category,
		Query:    query,
	})
}

// dressView backs a single garment's page.
type dressView struct {
	View
	Dress     Dress
	OrderURL  string
	Preview   string
	Related   []Dress
	MaxQty    int
	OrderData template.JS
}

// orderPayload is handed to the browser so the message preview can be rebuilt
// as the buyer changes size or quantity. The server still composes the message
// that is actually sent — this is only for showing.
type orderPayload struct {
	Template    string            `json:"template"`
	Fallback    string            `json:"fallback"`
	Values      map[string]string `json:"values"`
	SoldOut     bool              `json:"soldOut"`
	SoldOutNote string            `json:"soldOutNote"`

	// WaBase is the wa.me link up to and including "?text=". A statically
	// exported page has no server to compose the message for it, so the button
	// is a plain link and the script rewrites the whole address as the buyer
	// chooses. On the live shop the form is posted instead and this is unused.
	WaBase string `json:"waBase,omitempty"`
}

func (a *App) handleDress(w http.ResponseWriter, r *http.Request) {
	d, ok := a.store.BySlug(r.PathValue("slug"))
	if !ok {
		a.showError(w, r, http.StatusNotFound, "That dress is not here",
			"It may have been renamed or taken down. The lookbook has everything that is currently available.")
		return
	}
	d, err := a.store.RecordView(d.ID, a.memberFor(r).ID)
	if err != nil {
		slog.Error("record design view", "error", err)
	}
	set := a.store.Settings()
	v := a.newView(r, d.Name, d.Description)

	encoded := encodeOrderPayload(newOrderPayload(set, d))

	a.view.Render(w, http.StatusOK, "dress.html", dressView{
		View:      v,
		Dress:     d,
		OrderURL:  OrderURL(set, d, OrderRequest{}),
		Preview:   BuildOrderMessage(set, d, OrderRequest{}),
		Related:   relatedDresses(a.store.Dresses(), d, 3),
		MaxQty:    10,
		OrderData: encoded,
	})
}

func (a *App) handleDressReaction(w http.ResponseWriter, r *http.Request) {
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
	d, found := a.store.BySlug(r.PathValue("slug"))
	if !found {
		a.showError(w, r, http.StatusNotFound, "That design is not here", "It may have been taken down.")
		return
	}
	if _, err := a.store.ToggleReaction(d.ID, a.memberFor(r).ID); err != nil {
		slog.Error("save design reaction", "error", err)
		a.showError(w, r, http.StatusInternalServerError, "Your reaction could not be saved", "Please try again.")
		return
	}
	http.Redirect(w, r, d.Path(), http.StatusSeeOther)
}

// handleOrder is the hand-off to WhatsApp. A GET is the plain "order" button
// on a lookbook card; a POST carries the size, quantity and note chosen on
// the dress page. Either way the response is a redirect to wa.me with the
// message already written, so the buyer only presses send.
func (a *App) handleOrder(w http.ResponseWriter, r *http.Request) {
	d, ok := a.store.BySlug(r.PathValue("slug"))
	if !ok {
		a.showError(w, r, http.StatusNotFound, "That dress is not here",
			"It may have been renamed or taken down. The lookbook has everything that is currently available.")
		return
	}
	set := a.store.Settings()
	if !set.OrderingEnabled() {
		a.showError(w, r, http.StatusServiceUnavailable, "Ordering opens shortly",
			"The shop's WhatsApp number has not been connected yet. Please try again soon.")
		return
	}

	var req OrderRequest
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if err := r.ParseForm(); err != nil {
			a.showError(w, r, http.StatusBadRequest, "That order could not be read",
				"Please go back to the dress and try again.")
			return
		}
		qty, _ := strconv.Atoi(r.PostFormValue("qty"))
		req = OrderRequest{
			Size:  r.PostFormValue("size"),
			Qty:   qty,
			Notes: r.PostFormValue("notes"),
		}
	}

	// The redirect carries the buyer's own words in its query string, so it
	// must not be stored by a cache along the way.
	noStore(w)
	http.Redirect(w, r, OrderURL(set, d, req), http.StatusSeeOther)
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	a.showError(w, r, http.StatusNotFound, "Page not found",
		"The page you asked for is not part of this shop. Try the lookbook instead.")
}

func (a *App) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nDisallow: /admin\nDisallow: /order\nAllow: /\nSitemap: %s/sitemap.xml\n",
		a.origin(r))
}

// handleSitemap lists the storefront so a search engine can find every dress.
func (a *App) handleSitemap(w http.ResponseWriter, r *http.Request) {
	origin := a.origin(r)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	fmt.Fprintf(&b, "  <url><loc>%s/</loc></url>\n", origin)
	for _, d := range a.store.Dresses() {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc><lastmod>%s</lastmod></url>\n",
			origin, d.Path(), d.UpdatedAt.Format("2006-01-02"))
	}
	b.WriteString("</urlset>\n")
	_, _ = w.Write([]byte(b.String()))
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// origin is the public address of this shop: what the owner recorded in
// settings, or failing that whatever the request came in on.
func (a *App) origin(r *http.Request) string {
	if base := strings.TrimRight(a.store.Settings().BaseURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if isSecureRequest(r, a.trustProxy) {
		scheme = "https"
	}
	host := r.Host
	if a.trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
	}
	return scheme + "://" + host
}

// newOrderPayload gathers everything a browser needs to redraw the message
// preview locally: the template and the values its tokens stand for. The
// server still composes the message that is actually sent.
func newOrderPayload(set Settings, d Dress) orderPayload {
	image := ""
	if cover := d.Cover(); cover.Src != "" {
		image = set.AbsURL(cover.Src)
	}
	waBase := ""
	if set.OrderingEnabled() {
		waBase = "https://wa.me/" + set.WhatsApp + "?text="
	}
	return orderPayload{
		Template: set.MessageTemplate,
		Fallback: DefaultMessageTemplate,
		Values: map[string]string{
			"brand":    set.BrandName,
			"name":     d.Name,
			"price":    FormatMoney(set.Currency(), d.PriceMinor),
			"ref":      d.Ref,
			"image":    image,
			"link":     set.AbsURL(d.Path()),
			"fabric":   d.Fabric,
			"category": d.Category,
			"sizes":    strings.Join(d.Sizes, ", "),
			"delivery": set.DeliveryNote,
			"location": set.Location,
		},
		SoldOut:     d.SoldOut,
		SoldOutNote: soldOutNote,
		WaBase:      waBase,
	}
}

// encodeOrderPayload renders the payload for a data attribute. encoding/json
// escapes <, > and &, so nothing in it can break out of the markup.
func encodeOrderPayload(p orderPayload) template.JS {
	encoded, err := json.Marshal(p)
	if err != nil {
		slog.Error("encode order payload", "error", err)
		return template.JS("null")
	}
	return template.JS(encoded)
}
