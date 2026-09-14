package main

import (
	"strings"
	"testing"
)

func TestNormalizeWhatsApp(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		wantE bool
	}{
		{in: "", want: ""},    // clearing the number only pauses ordering
		{in: "   ", want: ""}, //
		{in: "2348155604988", want: "2348155604988"},
		{in: "+234 815 560 4988", want: "2348155604988"},
		{in: "00234-815-560-4988", want: "2348155604988"},
		{in: "(234) 815 560 4988", want: "2348155604988"},
		{in: "0815 560 4988", wantE: true},     // a local number, country code missing
		{in: "1234567", wantE: true},           // too short
		{in: "12345678901234567", wantE: true}, // too long
		{in: "call me", wantE: true},           // no digits at all
	}
	for _, c := range cases {
		got, err := NormalizeWhatsApp(c.in)
		switch {
		case c.wantE && err == nil:
			t.Errorf("NormalizeWhatsApp(%q) = %q, want an error", c.in, got)
		case !c.wantE && err != nil:
			t.Errorf("NormalizeWhatsApp(%q) returned %v", c.in, err)
		case !c.wantE && got != c.want:
			t.Errorf("NormalizeWhatsApp(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		wantE bool
	}{
		{in: "", want: ""},
		{in: "ajumafashionhub.com", want: "https://ajumafashionhub.com"},
		{in: "https://ajumafashionhub.com/", want: "https://ajumafashionhub.com"},
		{in: " https://ajumafashionhub.com ", want: "https://ajumafashionhub.com"},
		{in: "http://localhost:8080", want: "http://localhost:8080"},
		{in: "https://example.com/shop/", want: "https://example.com/shop"},
		{in: "https://example.com/shop?x=1#top", want: "https://example.com/shop"},
		{in: "https://", wantE: true},
		{in: "https://exa mple.com", wantE: true},
	}
	for _, c := range cases {
		got, err := NormalizeBaseURL(c.in)
		switch {
		case c.wantE && err == nil:
			t.Errorf("NormalizeBaseURL(%q) = %q, want an error", c.in, got)
		case !c.wantE && err != nil:
			t.Errorf("NormalizeBaseURL(%q) returned %v", c.in, err)
		case !c.wantE && got != c.want:
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAbsURL(t *testing.T) {
	off := Settings{}
	if got := off.AbsURL("/media/a.jpg"); got != "/media/a.jpg" {
		t.Errorf("without a public address AbsURL = %q, want the path unchanged", got)
	}
	if got := off.AbsURL(""); got != "" {
		t.Errorf("AbsURL(\"\") = %q, want nothing", got)
	}

	on := Settings{BaseURL: "https://ajumafashionhub.com/"}
	cases := map[string]string{
		"/media/a.jpg":              "https://ajumafashionhub.com/media/a.jpg",
		"media/a.jpg":               "https://ajumafashionhub.com/media/a.jpg",
		"https://cdn.example/a.jpg": "https://cdn.example/a.jpg",
	}
	for in, want := range cases {
		if got := on.AbsURL(in); got != want {
			t.Errorf("AbsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSettingsFallbacks(t *testing.T) {
	if got := (Settings{}).Currency(); got != "₦" {
		t.Errorf("Currency() = %q, want the naira sign", got)
	}
	if got := (Settings{CurrencySymbol: "$"}).Currency(); got != "$" {
		t.Errorf("Currency() = %q, want %q", got, "$")
	}
	if (Settings{}).OrderingEnabled() {
		t.Error("ordering should be off until a number is set")
	}
}

func TestSocialHandles(t *testing.T) {
	cases := map[string]string{
		"":                                "",
		"@ajumafashionhub":                "ajumafashionhub",
		"ajumafashionhub":                 "ajumafashionhub",
		"https://instagram.com/ajumafash": "ajumafash",
		"https://www.tiktok.com/@ajuma/":  "ajuma",
	}
	for in, want := range cases {
		if got := (Settings{Instagram: in}).InstagramHandle(); got != want {
			t.Errorf("InstagramHandle(%q) = %q, want %q", in, got, want)
		}
		if got := (Settings{TikTok: in}).TikTokHandle(); got != want {
			t.Errorf("TikTokHandle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every placeholder the settings page advertises has to be one the message
// builder actually fills, or the owner is being told about a word that does
// nothing.
func TestAdvertisedPlaceholdersAllResolve(t *testing.T) {
	set, d := testSettings(), testDress()
	for _, p := range messagePlaceholders {
		set.MessageTemplate = "before " + p.Token + " after"
		got := BuildOrderMessage(set, d, OrderRequest{Size: "M", Qty: 2, Notes: "a note"})
		if strings.Contains(got, p.Token) {
			t.Errorf("%s is offered on the settings page but never filled in", p.Token)
		}
	}
}

// The default message has to use the placeholders the shop is built around.
func TestDefaultTemplateUsesTheKeyPlaceholders(t *testing.T) {
	for _, token := range []string{"{brand}", "{name}", "{price}", "{ref}", "{options}", "{image}", "{link}"} {
		if !strings.Contains(DefaultMessageTemplate, token) {
			t.Errorf("the default message no longer carries %s", token)
		}
	}
}
