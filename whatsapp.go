package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Limits on what a buyer can push into the message. WhatsApp accepts long
// pre-filled text, but a URL that grows without bound is a liability.
const (
	maxNoteRunes    = 400
	maxMessageRunes = 1800
	maxQuantity     = 20
)

// soldOutNote is added to a message for a piece that is marked sold out, so
// the buyer's first message already asks the only question worth asking.
const soldOutNote = "This piece is marked sold out — please let me know if it can be made again."

// OrderRequest is the buyer's choice on the dress page. Every field is
// optional — the plain "Order" button sends an empty request.
type OrderRequest struct {
	Size  string
	Qty   int
	Notes string
}

// Clean trims, de-fangs and clamps whatever arrived from the form.
func (o OrderRequest) Clean() OrderRequest {
	o.Size = trimTo(oneLine(o.Size), 40)
	o.Notes = trimTo(stripControl(o.Notes), maxNoteRunes)
	if o.Qty < 1 {
		o.Qty = 1
	}
	if o.Qty > maxQuantity {
		o.Qty = maxQuantity
	}
	return o
}

// OrderLines renders the buyer's choices, one labelled line each. It is what
// the {options} placeholder expands to, and what the live preview on the
// dress page shows before anything is sent.
func (o OrderRequest) OrderLines() string {
	var lines []string
	if o.Size != "" {
		lines = append(lines, "Size: "+o.Size)
	}
	if o.Qty > 1 {
		lines = append(lines, "Quantity: "+strconv.Itoa(o.Qty))
	}
	if o.Notes != "" {
		lines = append(lines, "Note: "+o.Notes)
	}
	return strings.Join(lines, "\n")
}

// BuildOrderMessage fills the owner's template for one dress and one buyer.
// A placeholder that resolves to nothing leaves no empty line behind.
func BuildOrderMessage(set Settings, d Dress, req OrderRequest) string {
	req = req.Clean()
	tmpl := set.MessageTemplate
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultMessageTemplate
	}

	image := ""
	if cover := d.Cover(); cover.Src != "" {
		image = set.AbsURL(cover.Src)
	}

	options := req.OrderLines()
	if d.SoldOut {
		// A one-woman atelier can usually cut a sold-out piece again, so the
		// enquiry is still worth sending — it just has to say so.
		note := soldOutNote
		if options == "" {
			options = note
		} else {
			options += "\n" + note
		}
	}

	replacer := strings.NewReplacer(
		"{brand}", set.BrandName,
		"{name}", d.Name,
		"{price}", FormatMoney(set.Currency(), d.PriceMinor),
		"{ref}", d.Ref,
		"{options}", options,
		"{image}", image,
		"{link}", set.AbsURL(d.Path()),
		"{fabric}", d.Fabric,
		"{category}", d.Category,
		"{sizes}", strings.Join(d.Sizes, ", "),
		"{delivery}", set.DeliveryNote,
		"{location}", set.Location,
	)
	return trimTo(collapseBlankLines(replacer.Replace(tmpl)), maxMessageRunes)
}

// OrderURL is the wa.me deep link that opens WhatsApp with the message
// already typed, leaving the buyer nothing to do but press send.
//
// Note on the image: WhatsApp's click-to-chat API carries text only — no
// attachment can be pre-loaded. The message therefore carries the dress
// photograph's public URL, which WhatsApp expands into a picture preview
// once the site is reachable from the internet. Until the owner records a
// public address in Settings the link is site-relative and no preview
// appears, which is why the admin nags about it.
func OrderURL(set Settings, d Dress, req OrderRequest) string {
	if !set.OrderingEnabled() {
		return ""
	}
	return "https://wa.me/" + set.WhatsApp + "?text=" + encodeMessage(BuildOrderMessage(set, d, req))
}

// ChatURL opens a plain conversation with the shop, no dress attached.
func ChatURL(set Settings) string {
	if !set.OrderingEnabled() {
		return ""
	}
	text := fmt.Sprintf("Hello %s! I have a question about your dresses.", set.BrandName)
	return "https://wa.me/" + set.WhatsApp + "?text=" + encodeMessage(text)
}

// DisplayNumber spaces a raw number out for reading: 2348155604988 ->
// "+234 815 560 4988".
func DisplayNumber(digits string) string {
	if digits == "" {
		return ""
	}
	var parts []string
	for i := 0; i < len(digits); i += 3 {
		end := min(i+3, len(digits))
		parts = append(parts, digits[i:end])
	}
	// A trailing single digit reads as a typo, so it joins the group before it:
	// 234 815 560 498 8 becomes 234 815 560 4988.
	if n := len(parts); n > 1 && len(parts[n-1]) == 1 {
		parts[n-2] += parts[n-1]
		parts = parts[:n-1]
	}
	return "+" + strings.Join(parts, " ")
}

// encodeMessage percent-encodes the text for a query string. url.QueryEscape
// turns a space into "+", which some WhatsApp clients show literally, so the
// spaces are re-encoded as %20.
func encodeMessage(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// collapseBlankLines squeezes runs of empty lines down to one, which is what
// turns an unused placeholder from a hole in the message into nothing at all.
func collapseBlankLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if blank++; blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// oneLine flattens whitespace, including newlines, into single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// stripControl removes control characters a form should never send, but
// keeps the newlines a buyer may have typed into a note.
func stripControl(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 {
			return r
		}
		return -1
	}, strings.TrimSpace(s))
}

// trimTo caps a string at n runes without splitting one in half.
func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}
