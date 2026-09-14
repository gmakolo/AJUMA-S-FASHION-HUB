package main

import (
	"strings"
	"testing"
)

func TestParsePrice(t *testing.T) {
	cases := []struct {
		in    string
		want  int64
		wantE bool
	}{
		{in: "28500", want: 2850000},
		{in: "28,500", want: 2850000},
		{in: "₦28,500", want: 2850000},
		{in: " 28 500 ", want: 2850000},
		{in: "28500.50", want: 2850050},
		{in: "28500.5", want: 2850050},
		{in: "28500.", want: 2850000},
		{in: ".50", want: 50},
		{in: "", wantE: true},
		{in: "free", wantE: true},
		{in: "0", wantE: true},
		{in: "0.00", wantE: true},
		{in: "1.2.3", wantE: true},
		{in: "99999999999", wantE: true},
	}
	for _, c := range cases {
		got, err := ParsePrice(c.in)
		switch {
		case c.wantE && err == nil:
			t.Errorf("ParsePrice(%q) = %d, want an error", c.in, got)
		case !c.wantE && err != nil:
			t.Errorf("ParsePrice(%q) returned %v", c.in, err)
		case !c.wantE && got != c.want:
			t.Errorf("ParsePrice(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A price the owner typed must survive the round trip back to the page.
func TestPriceRoundTrip(t *testing.T) {
	for _, typed := range []string{"28500", "1,250.75", "9", "999999999"} {
		minor, err := ParsePrice(typed)
		if err != nil {
			t.Fatalf("ParsePrice(%q): %v", typed, err)
		}
		shown := FormatMoney("", minor)
		again, err := ParsePrice(shown)
		if err != nil {
			t.Fatalf("ParsePrice(%q) after formatting: %v", shown, err)
		}
		if again != minor {
			t.Errorf("%q formatted to %q and reparsed as %d, want %d", typed, shown, again, minor)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{0, "₦0"},
		{50, "₦0.50"},
		{100, "₦1"},
		{99900, "₦999"},
		{100000, "₦1,000"},
		{2850000, "₦28,500"},
		{2850050, "₦28,500.50"},
		{2850005, "₦28,500.05"},
		{123456789000, "₦1,234,567,890"},
		{-2850000, "-₦28,500"},
	}
	for _, c := range cases {
		if got := FormatMoney("₦", c.minor); got != c.want {
			t.Errorf("FormatMoney(%d) = %q, want %q", c.minor, got, c.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Adaeze Wrap Dress":    "adaeze-wrap-dress",
		"Zuri — Column Gown!":  "zuri-column-gown",
		"  Kachi   Kaftan  ":   "kachi-kaftan",
		"Two-Piece No. 6":      "two-piece-no-6",
		"ÀJÚMÀ":                "ajuma",
		"Àmàká Wrap Dress":     "amaka-wrap-dress",
		"":                     "",
		"!!!":                  "",
		"Ronke 2 Piece (2026)": "ronke-2-piece-2026",
	}
	// Accents arriving as separate combining marks must not split the word.
	decomposed := string([]rune{'O', 0x323, 0x300, 's', 0x323, 'u', 'n', ' ', 'G', 'o', 'w', 'n'})
	if got := Slugify(decomposed); got != "osun-gown" {
		t.Errorf("Slugify of decomposed text = %q, want %q", got, "osun-gown")
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt("  a\n\n b   c ", 40); got != "a b c" {
		t.Errorf("Excerpt collapsed to %q, want %q", got, "a b c")
	}
	got := Excerpt("The quick brown fox jumps over the lazy dog", 12)
	if got != "The quick…" {
		t.Errorf("Excerpt(…, 12) = %q, want %q", got, "The quick…")
	}
	if got := Excerpt("short", 40); got != "short" {
		t.Errorf("Excerpt left %q alone, got %q", "short", got)
	}
	if got := Excerpt("électrique", 4); !strings.HasSuffix(got, "…") || len([]rune(got)) > 5 {
		t.Errorf("Excerpt split a multi-byte rune: %q", got)
	}
}

func TestParseList(t *testing.T) {
	got := ParseList("S, M , M / L | XL\nXXL\n\n")
	want := []string{"S", "M", "L", "XL", "XXL"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ParseList = %v, want %v", got, want)
	}
	if got := ParseList("  ,  , "); got != nil {
		t.Errorf("ParseList of blanks = %v, want nil", got)
	}
}

func TestDressHelpers(t *testing.T) {
	bare := Dress{Name: "Bare", Slug: "bare"}
	if bare.HasImage() || bare.Cover() != (Image{}) || bare.Extras() != nil {
		t.Error("a dress with no photographs should report none")
	}
	if !bare.Available() {
		t.Error("a dress that is not sold out should be available")
	}
	if bare.Path() != "/dress/bare" {
		t.Errorf("Path() = %q, want /dress/bare", bare.Path())
	}

	dressed := Dress{Slug: "zuri", SoldOut: true, Images: []Image{
		{Src: "/media/a.jpg"}, {Src: "/media/b.jpg"}, {Src: "/media/c.jpg"},
	}}
	if dressed.Cover().Src != "/media/a.jpg" {
		t.Errorf("Cover() = %q, want the first photograph", dressed.Cover().Src)
	}
	if len(dressed.Extras()) != 2 || dressed.Extras()[0].Src != "/media/b.jpg" {
		t.Errorf("Extras() = %v, want everything after the cover", dressed.Extras())
	}
	if dressed.Available() {
		t.Error("a sold-out dress should not be available")
	}
}

func TestNewIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := NewID("d_")
		if !strings.HasPrefix(id, "d_") {
			t.Fatalf("NewID lost its prefix: %q", id)
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
	}
}
