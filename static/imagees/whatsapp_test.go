package main

import (
	"net/url"
	"strings"
	"testing"
)

// testSettings is a shop with everything filled in, so a test can knock out
// only the field it cares about.
func testSettings() Settings {
	set := DefaultSettings()
	set.WhatsApp = "2348155604988"
	set.BaseURL = "https://ajumafashionhub.com"
	return set
}

func testDress() Dress {
	return Dress{
		ID:          "d_1",
		Ref:         "AJM-001",
		Name:        "Adaeze Wrap Dress",
		Slug:        "adaeze-wrap-dress",
		Description: "A true wrap that ties at the waist.",
		Fabric:      "Ankara cotton",
		Category:    "Wrap",
		PriceMinor:  3450000,
		Sizes:       []string{"S", "M", "L"},
		Images:      []Image{{Src: "/media/adaeze.jpg"}},
	}
}

func TestOrderRequestClean(t *testing.T) {
	got := OrderRequest{Size: " M \n L ", Qty: 0, Notes: "  sleeves\x00 a little longer\r\n please  "}.Clean()
	if got.Size != "M L" {
		t.Errorf("Size = %q, want %q", got.Size, "M L")
	}
	if got.Qty != 1 {
		t.Errorf("Qty = %d, want 1", got.Qty)
	}
	if got.Notes != "sleeves a little longer\n please" {
		t.Errorf("Notes = %q", got.Notes)
	}
	if q := (OrderRequest{Qty: 500}).Clean().Qty; q != maxQuantity {
		t.Errorf("Qty = %d, want it clamped to %d", q, maxQuantity)
	}
	long := OrderRequest{Notes: strings.Repeat("é", maxNoteRunes+200)}.Clean()
	if n := len([]rune(long.Notes)); n != maxNoteRunes {
		t.Errorf("a long note kept %d runes, want %d", n, maxNoteRunes)
	}
	wide := OrderRequest{Size: strings.Repeat("x", 90)}.Clean()
	if n := len(wide.Size); n != 40 {
		t.Errorf("a long size kept %d characters, want 40", n)
	}
}

func TestOrderLines(t *testing.T) {
	if got := (OrderRequest{}).Clean().OrderLines(); got != "" {
		t.Errorf("an empty order produced %q, want nothing", got)
	}
	if got := (OrderRequest{Size: "M"}).Clean().OrderLines(); got != "Size: M" {
		t.Errorf("OrderLines = %q, want %q", got, "Size: M")
	}
	// A quantity of one is the assumption, so it is not worth a line.
	if got := (OrderRequest{Qty: 1}).Clean().OrderLines(); got != "" {
		t.Errorf("OrderLines = %q, want nothing for a single dress", got)
	}
	got := OrderRequest{Size: "L", Qty: 3, Notes: "for a wedding"}.Clean().OrderLines()
	want := "Size: L\nQuantity: 3\nNote: for a wedding"
	if got != want {
		t.Errorf("OrderLines =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildOrderMessage(t *testing.T) {
	set, d := testSettings(), testDress()
	msg := BuildOrderMessage(set, d, OrderRequest{Size: "M", Qty: 2, Notes: "longer sleeves"})

	for _, want := range []string{
		"Ajuma Fashion Hub",
		"Adaeze Wrap Dress",
		"₦34,500",
		"AJM-001",
		"Size: M",
		"Quantity: 2",
		"Note: longer sleeves",
		"https://ajumafashionhub.com/media/adaeze.jpg",
		"https://ajumafashionhub.com/dress/adaeze-wrap-dress",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message is missing %q:\n%s", want, msg)
		}
	}
	if strings.ContainsAny(msg, "{}") {
		t.Errorf("a placeholder was left unfilled:\n%s", msg)
	}
}

// An unused placeholder must not leave a hole in the message.
func TestBuildOrderMessageLeavesNoBlankLines(t *testing.T) {
	set := testSettings()
	set.BaseURL = "" // no public address, so {image} resolves to nothing
	d := testDress()
	d.Images = nil

	msg := BuildOrderMessage(set, d, OrderRequest{})
	if strings.Contains(msg, "\n\n\n") {
		t.Errorf("the message has a run of blank lines:\n%q", msg)
	}
	if strings.HasPrefix(msg, "\n") || strings.HasSuffix(msg, "\n") {
		t.Errorf("the message is padded with newlines:\n%q", msg)
	}
	// Without a public address the link stays site-relative rather than broken.
	if !strings.Contains(msg, "/dress/adaeze-wrap-dress") {
		t.Errorf("the message lost its link:\n%s", msg)
	}
}

func TestBuildOrderMessageSoldOut(t *testing.T) {
	set, d := testSettings(), testDress()
	d.SoldOut = true
	if msg := BuildOrderMessage(set, d, OrderRequest{}); !strings.Contains(msg, soldOutNote) {
		t.Errorf("a sold-out dress should ask whether it can be made again:\n%s", msg)
	}
	msg := BuildOrderMessage(set, d, OrderRequest{Size: "M"})
	if !strings.Contains(msg, "Size: M\n"+soldOutNote) {
		t.Errorf("the sold-out note should follow the buyer's choices:\n%s", msg)
	}
}

func TestBuildOrderMessageTemplates(t *testing.T) {
	set, d := testSettings(), testDress()

	set.MessageTemplate = "   \n  "
	if msg := BuildOrderMessage(set, d, OrderRequest{}); !strings.Contains(msg, "I would like to order") {
		t.Errorf("a blank template should fall back to the default:\n%s", msg)
	}

	set.MessageTemplate = "{name} — {fabric} — {sizes} — {category} — {delivery} — {location}"
	msg := BuildOrderMessage(set, d, OrderRequest{})
	if !strings.Contains(msg, "Ankara cotton") || !strings.Contains(msg, "S, M, L") {
		t.Errorf("the optional placeholders did not fill:\n%s", msg)
	}

	// A word the shop does not know is left exactly as typed, so the owner can
	// see the mistake in the preview instead of losing text silently.
	set.MessageTemplate = "Hello {brand}, about {nonsense}"
	if msg := BuildOrderMessage(set, d, OrderRequest{}); !strings.Contains(msg, "{nonsense}") {
		t.Errorf("an unknown placeholder should survive untouched:\n%s", msg)
	}
}

func TestBuildOrderMessageIsClamped(t *testing.T) {
	set, d := testSettings(), testDress()
	set.MessageTemplate = strings.Repeat("é", maxMessageRunes+500)
	if n := len([]rune(BuildOrderMessage(set, d, OrderRequest{}))); n != maxMessageRunes {
		t.Errorf("the message kept %d runes, want %d", n, maxMessageRunes)
	}
}

func TestOrderURL(t *testing.T) {
	set, d := testSettings(), testDress()
	req := OrderRequest{Size: "M", Qty: 2}

	link := OrderURL(set, d, req)
	prefix := "https://wa.me/2348155604988?text="
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("OrderURL = %q, want it to start with %q", link, prefix)
	}
	if strings.ContainsAny(link, " \"<>") {
		t.Errorf("OrderURL is not escaped: %q", link)
	}
	// url.QueryEscape spells a space "+", which some clients show literally.
	if strings.Contains(link, "+") {
		t.Errorf("OrderURL should encode a space as %%20, not +: %q", link)
	}

	decoded, err := url.QueryUnescape(strings.TrimPrefix(link, prefix))
	if err != nil {
		t.Fatalf("the query could not be decoded: %v", err)
	}
	if decoded != BuildOrderMessage(set, d, req) {
		t.Errorf("the link carries a different message:\n%q", decoded)
	}
}

func TestOrderURLNeedsANumber(t *testing.T) {
	set, d := testSettings(), testDress()
	set.WhatsApp = ""
	if set.OrderingEnabled() {
		t.Fatal("ordering should be off without a number")
	}
	if link := OrderURL(set, d, OrderRequest{}); link != "" {
		t.Errorf("OrderURL = %q, want nothing while ordering is off", link)
	}
	if link := ChatURL(set); link != "" {
		t.Errorf("ChatURL = %q, want nothing while ordering is off", link)
	}
}

func TestChatURL(t *testing.T) {
	link := ChatURL(testSettings())
	if !strings.HasPrefix(link, "https://wa.me/2348155604988?text=") {
		t.Fatalf("ChatURL = %q", link)
	}
	decoded, err := url.QueryUnescape(strings.SplitN(link, "text=", 2)[1])
	if err != nil {
		t.Fatalf("the query could not be decoded: %v", err)
	}
	if !strings.Contains(decoded, "Ajuma Fashion Hub") {
		t.Errorf("the greeting does not name the shop: %q", decoded)
	}
}

func TestDisplayNumber(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"2348155604988": "+234 815 560 4988",
		"447700900123":  "+447 700 900 123",
		"12025550147":   "+120 255 501 47",
	}
	for in, want := range cases {
		if got := DisplayNumber(in); got != want {
			t.Errorf("DisplayNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollapseBlankLines(t *testing.T) {
	got := collapseBlankLines("\n\nfirst\r\n\n\n\nsecond   \n\n")
	if got != "first\n\nsecond" {
		t.Errorf("collapseBlankLines = %q, want %q", got, "first\n\nsecond")
	}
}
