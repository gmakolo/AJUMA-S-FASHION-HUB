package main

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
)

// placeholder documents one token the owner may use in the order message.
type placeholder struct{ Token, Meaning string }

var messagePlaceholders = []placeholder{
	{"{brand}", "your shop name"},
	{"{name}", "the name of the dress"},
	{"{price}", "the price, formatted"},
	{"{ref}", "the reference code, e.g. AJM-004"},
	{"{options}", "the size, quantity and note the buyer chose"},
	{"{image}", "a link to the photograph — WhatsApp turns this into a picture preview"},
	{"{link}", "a link to the dress on your site"},
	{"{fabric}", "the fabric line"},
	{"{category}", "the category"},
	{"{sizes}", "every size you listed"},
	{"{delivery}", "your delivery note"},
	{"{location}", "your location"},
}

type adminSettingsView struct {
	AdminView
	Set           Settings
	Errors        []string
	NumberDisplay string
	Preview       string
	PreviewOf     string
	Placeholders  []placeholder
	OrderData     template.JS
}

func (a *App) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	a.renderSettings(w, r, http.StatusOK, a.store.Settings(), nil)
}

// renderSettings draws the settings page, with a live example of the message a
// buyer would send so the owner can see the effect of a change immediately.
func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, status int,
	set Settings, errs []string) {

	sample, previewOf := a.previewDress()
	a.view.Render(w, status, "admin_settings.html", adminSettingsView{
		AdminView:     a.adminView(r, "Settings"),
		Set:           set,
		Errors:        errs,
		NumberDisplay: DisplayNumber(set.WhatsApp),
		Preview:       BuildOrderMessage(set, sample, OrderRequest{Size: "M", Qty: 1}),
		PreviewOf:     previewOf,
		Placeholders:  messagePlaceholders,
		OrderData:     encodeOrderPayload(newOrderPayload(set, sample)),
	})
}

// previewDress is a real dress if there is one, otherwise a stand-in so the
// message preview has something to describe.
func (a *App) previewDress() (Dress, string) {
	if all := a.store.Dresses(); len(all) > 0 {
		return all[0], all[0].Name
	}
	return Dress{
		Name:        "Ojoma Achi Set",
		Ref:         "AJM-001",
		PriceMinor:  5200000,
		Fabric:      "Handwoven achi in gold, black, white and teal",
		Category:    "Wrapper",
		Sizes:       []string{"S", "M", "L"},
		Description: "An example dress, shown until you add your first one.",
	}, "an example dress"
}

func (a *App) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !a.simplePost(w, r) {
		return
	}
	set := a.store.Settings()
	var errs []string

	set.BrandName = trimTo(oneLine(r.PostFormValue("brand_name")), 60)
	if set.BrandName == "" {
		errs = append(errs, "The shop needs a name.")
	}
	set.Tagline = trimTo(oneLine(r.PostFormValue("tagline")), 160)
	set.Story = trimTo(stripControl(r.PostFormValue("story")), 1200)
	set.Location = trimTo(oneLine(r.PostFormValue("location")), 80)
	set.Email = trimTo(oneLine(r.PostFormValue("email")), 120)
	set.Instagram = trimTo(oneLine(r.PostFormValue("instagram")), 120)
	set.TikTok = trimTo(oneLine(r.PostFormValue("tiktok")), 120)
	set.DeliveryNote = trimTo(oneLine(r.PostFormValue("delivery_note")), 200)
	set.Announcement = trimTo(oneLine(r.PostFormValue("announcement")), 160)
	if symbol := trimTo(oneLine(r.PostFormValue("currency_symbol")), 4); symbol != "" {
		set.CurrencySymbol = symbol
	}

	if template := strings.TrimSpace(stripControl(r.PostFormValue("message_template"))); template == "" {
		set.MessageTemplate = DefaultMessageTemplate
	} else {
		set.MessageTemplate = trimTo(template, 1200)
	}

	number, err := NormalizeWhatsApp(r.PostFormValue("whatsapp"))
	if err != nil {
		errs = append(errs, sentence(err.Error()))
	} else {
		set.WhatsApp = number
	}

	base, err := NormalizeBaseURL(r.PostFormValue("base_url"))
	if err != nil {
		errs = append(errs, sentence(err.Error()))
	} else {
		set.BaseURL = base
	}

	if len(errs) > 0 {
		a.renderSettings(w, r, http.StatusUnprocessableEntity, set, errs)
		return
	}
	if err := a.store.SaveSettings(set); err != nil {
		slog.Error("save settings", "error", err)
		a.renderSettings(w, r, http.StatusInternalServerError, set,
			[]string{"Those settings could not be saved. Please try again."})
		return
	}

	token := a.sessionToken(r)
	if !strings.Contains(set.MessageTemplate, "{name}") {
		a.sessions.Flash(token, "warn",
			"Your order message no longer mentions {name}, so buyers will not say which dress they want.")
	}
	if !strings.Contains(set.MessageTemplate, "{image}") && !strings.Contains(set.MessageTemplate, "{link}") {
		a.sessions.Flash(token, "warn",
			"Your order message has no {image} or {link}, so WhatsApp cannot show the dress.")
	}
	a.sessions.Flash(token, "ok", "Settings saved.")
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}
