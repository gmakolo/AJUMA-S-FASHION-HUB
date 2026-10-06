// Ajuma Fashion Hub — a dependency-free storefront that turns a garment
// into a ready-to-send WhatsApp order.
//
// The whole application is the Go standard library: net/http for routing,
// html/template for rendering, encoding/json for storage, image/* for
// processing uploads. No third-party packages, no database daemon.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dress is one garment in the lookbook. Money is held in minor units
// (kobo) so no price ever passes through a float.
type Dress struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id,omitempty"`
	OwnerName   string    `json:"owner_name,omitempty"`
	Ref         string    `json:"ref"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Fabric      string    `json:"fabric,omitempty"`
	Category    string    `json:"category,omitempty"`
	PriceMinor  int64     `json:"price_minor"`
	Sizes       []string  `json:"sizes,omitempty"`
	Images      []Image   `json:"images,omitempty"`
	Viewers     []string  `json:"viewers,omitempty"`
	Reactions   []string  `json:"reactions,omitempty"`
	Featured    bool      `json:"featured"`
	SoldOut     bool      `json:"sold_out"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Image is one photograph of a dress. Tint holds the average colour of the
// file so the grid can paint a matching placeholder instead of flashing
// white while the photograph loads.
type Image struct {
	Src  string `json:"src"`
	Alt  string `json:"alt,omitempty"`
	W    int    `json:"w,omitempty"`
	H    int    `json:"h,omitempty"`
	Tint string `json:"tint,omitempty"`
}

// Cover is the primary photograph, or the zero Image when none was uploaded.
func (d Dress) Cover() Image {
	if len(d.Images) == 0 {
		return Image{}
	}
	return d.Images[0]
}

// Extras are the photographs shown after the cover on the detail page.
func (d Dress) Extras() []Image {
	if len(d.Images) < 2 {
		return nil
	}
	return d.Images[1:]
}

func (d Dress) HasImage() bool { return len(d.Images) > 0 }

func (d Dress) ViewCount() int     { return len(d.Viewers) }
func (d Dress) ReactionCount() int { return len(d.Reactions) }
func (d Dress) ReactedBy(memberID string) bool {
	for _, id := range d.Reactions {
		if id == memberID {
			return true
		}
	}
	return false
}

// Available reports whether the dress can still be ordered.
func (d Dress) Available() bool { return !d.SoldOut }

// Path is the canonical storefront URL for the dress.
func (d Dress) Path() string { return "/dress/" + d.Slug }

// MaxPriceMinor caps a price at ten billion minor units, which keeps a
// mistyped figure from overflowing anything downstream.
const MaxPriceMinor = 1_000_000_000_00

// ParsePrice reads a price a human typed. It tolerates currency symbols,
// thousands separators and stray spaces: "N28,500", "28500", "28,500.50".
func ParsePrice(s string) (int64, error) {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' {
			return r
		}
		return -1
	}, s)
	if cleaned == "" {
		return 0, errors.New("enter a price, for example 28500")
	}
	if strings.Count(cleaned, ".") > 1 {
		return 0, errors.New("that price has more than one decimal point")
	}
	whole, frac, _ := strings.Cut(cleaned, ".")
	if whole == "" {
		whole = "0"
	}
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, errors.New("that price is not a number")
	}
	minor, err := strconv.ParseInt((frac + "00")[:2], 10, 64)
	if err != nil {
		return 0, errors.New("that price is not a number")
	}
	total := major*100 + minor
	if total <= 0 {
		return 0, errors.New("a price has to be greater than zero")
	}
	if total > MaxPriceMinor {
		return 0, errors.New("that price is unrealistically large")
	}
	return total, nil
}

// FormatMoney renders minor units for display: 2850000 becomes "N28,500",
// and a non-zero remainder keeps both decimals: 2850050 -> "N28,500.50".
func FormatMoney(symbol string, minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	out := sign + symbol + groupThousands(minor/100)
	if rem := minor % 100; rem != 0 {
		out += fmt.Sprintf(".%02d", rem)
	}
	return out
}

// groupThousands turns 28500 into "28,500".
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	if lead := len(s) % 3; lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := len(s) % 3; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Slugify reduces a name to a URL-safe fragment: "Ada Wrap Dress" -> "ada-wrap-dress".
func Slugify(s string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if r >= 0x300 && r <= 0x36f {
			continue // a combining accent arriving on its own
		}
		if folded, ok := asciiFold[r]; ok {
			r = folded
		}
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		default:
			pendingDash = true
		}
	}
	return b.String()
}

// NewID returns a prefixed random identifier, e.g. "d_9f2ac1b40e7d".
func NewID(prefix string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice; the clock keeps us unique
		// enough if it ever does rather than handing back a duplicate id.
		return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + hex.EncodeToString(buf)
}

// Excerpt trims text to roughly max runes, breaking on a word boundary.
func Excerpt(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)[:max]
	if i := strings.LastIndexByte(string(r), ' '); i > max/2 {
		return strings.TrimRight(string(r)[:i], " ,.;:-") + "…"
	}
	return strings.TrimRight(string(r), " ,.;:-") + "…"
}

// ParseList splits a comma or newline separated field into trimmed values,
// dropping blanks and duplicates while keeping the order the owner typed.
func ParseList(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '/' || r == '|'
	}) {
		v := strings.TrimSpace(part)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	return out
}

// asciiFold folds the accented Latin letters a dress name may carry down to
// the letter underneath, so "Àmàká" slugs as "amaka" rather than losing half
// its letters. The first rune of each group is what the rest become.
var asciiFold = foldTable(
	"aàáâãäåāăą", "cçćĉċč", "dđďḍ", "eèéêëēĕėęěẹ", "gĝğġģ",
	"iìíîïĩīĭįị", "lĺļľł", "nñńņňṅ", "oòóôõöøōŏőọ", "sśŝşšṣ",
	"tţťṭ", "uùúûüũūŭůųụ", "wŵẁẃ", "yýÿŷ", "zźżž",
)

func foldTable(groups ...string) map[rune]rune {
	table := map[rune]rune{}
	for _, group := range groups {
		runes := []rune(group)
		for _, r := range runes[1:] {
			table[r] = runes[0]
		}
	}
	return table
}
