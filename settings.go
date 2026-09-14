package main

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Settings is everything the owner can change without touching code.
type Settings struct {
	BrandName       string    `json:"brand_name"`
	Tagline         string    `json:"tagline"`
	Story           string    `json:"story"`
	WhatsApp        string    `json:"whatsapp"` // digits only, country code first
	MessageTemplate string    `json:"message_template"`
	BaseURL         string    `json:"base_url"` // public origin, used for absolute links
	CurrencySymbol  string    `json:"currency_symbol"`
	Location        string    `json:"location"`
	Email           string    `json:"email"`
	Instagram       string    `json:"instagram"`
	TikTok          string    `json:"tiktok"`
	DeliveryNote    string    `json:"delivery_note"`
	Announcement    string    `json:"announcement"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DefaultMessageTemplate is the WhatsApp message a buyer sends. Placeholders
// are substituted at order time; a placeholder that resolves to nothing takes
// its whole line with it, so the message never has an orphaned label.
const DefaultMessageTemplate = `Hello {brand}!

I would like to order this dress 👇

*{name}*
{price}  ·  Ref {ref}
{options}
{image}

Details: {link}

Please confirm availability and delivery to my area. Thank you!`

// DefaultSettings is what a fresh install starts with.
func DefaultSettings() Settings {
	return Settings{
		BrandName: "Ajuma Fashion Hub",
		Tagline:   "Dresses cut, sewn and finished by hand — one piece at a time.",
		Story: "Ajuma Fashion Hub is a one-woman atelier. Every dress on this page was " +
			"drafted, cut and sewn in our studio, in small runs or as a single piece. " +
			"Choose what you love, send the message, and we take it from there.",
		MessageTemplate: DefaultMessageTemplate,
		CurrencySymbol:  "₦",
		Location:        "Lagos, Nigeria",
		DeliveryNote:    "Nationwide delivery. Made-to-measure takes 5–10 days.",
		Announcement:    "Made to order · Nationwide delivery · Ask about your measurements",
		UpdatedAt:       time.Now(),
	}
}

// OrderingEnabled reports whether a buyer can be sent to WhatsApp yet.
func (s Settings) OrderingEnabled() bool { return s.WhatsApp != "" }

// Currency falls back to the naira sign if the field was cleared.
func (s Settings) Currency() string {
	if s.CurrencySymbol == "" {
		return "₦"
	}
	return s.CurrencySymbol
}

// AbsURL turns a site-relative path into an absolute URL when the owner has
// recorded the public origin. Without it the path is returned unchanged —
// still correct in a browser, just not previewable inside WhatsApp.
func (s Settings) AbsURL(path string) string {
	if path == "" {
		return ""
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		return path
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// InstagramHandle strips a pasted URL or leading @ down to the handle.
func (s Settings) InstagramHandle() string { return socialHandle(s.Instagram) }

// TikTokHandle strips a pasted URL or leading @ down to the handle.
func (s Settings) TikTokHandle() string { return socialHandle(s.TikTok) }

func socialHandle(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.LastIndexByte(strings.TrimRight(v, "/"), '/'); i >= 0 {
		v = strings.TrimRight(v, "/")[i+1:]
	}
	return strings.TrimPrefix(v, "@")
}

// NormalizeWhatsApp reduces a typed number to the digits WhatsApp expects:
// country code first, no plus, no spaces. It refuses a local number rather
// than guessing a country code on the owner's behalf.
func NormalizeWhatsApp(s string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	digits = strings.TrimPrefix(digits, "00")
	switch {
	case strings.TrimSpace(s) == "":
		return "", nil // clearing the number is allowed; it just pauses ordering
	case digits == "":
		return "", errors.New("that does not look like a phone number — digits only, country code first, for example 234 802 000 0000")
	case strings.HasPrefix(digits, "0"):
		return "", errors.New("that looks like a local number — replace the leading 0 with your country code, for example 234 802 000 0000")
	case len(digits) < 8:
		return "", errors.New("that number is too short to be a WhatsApp number")
	case len(digits) > 15:
		return "", errors.New("that number is too long to be a WhatsApp number")
	}
	return digits, nil
}

// NormalizeBaseURL validates the public origin the owner pasted.
func NormalizeBaseURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, " \t\n\"'<>") {
		return "", errors.New("that web address contains characters a URL cannot hold")
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	parsed, err := url.Parse(s)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("that web address could not be read — it should look like https://ajumafashionhub.com")
	}
	// Keep the scheme, the host and any sub-path, and drop the rest: a query
	// or a fragment here would only break every link built on top of it.
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.Path, "/"), nil
}
