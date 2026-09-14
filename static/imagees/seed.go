package main

import (
	"log/slog"
	"net/http"
)

// sampleDresses is a small collection used to fill an empty shop, so the owner
// can see what the storefront looks like before photographing anything. The
// photographs are shipped inside the binary and were put through the same
// pipeline as an owner's upload; every field is editable afterwards, and the
// whole set can be deleted once real dresses arrive. Their sources and licences
// are listed in static/img/lookbook/CREDITS.md.
var sampleDresses = []Dress{
	{
		Name:        "Ojoma Achi Set",
		Description: "Achi, the handwoven cloth of Igalaland, made up the way it is usually worn: a full wrapper, a matching ipele for the shoulder, and a length left long enough to tie the head. The gold ground carries one green stripe, and the black bands — the colour Igala keeps for the richness of its own land — are picked out in fine teal and white.",
		Fabric:      "Handwoven achi in gold, black, white and teal",
		Category:    "Wrapper",
		PriceMinor:  5200000,
		Sizes:       []string{"S", "M", "L", "XL"},
		Featured:    true,
		Images:      []Image{{Src: "/static/img/lookbook/01.jpg", Alt: "Handwoven Igala achi cloth in gold, black and white, folded in three lengths", W: 1050, H: 1400, Tint: "#9a8b5b"}},
	},
	{
		Name:        "Zuri Column Gown",
		Description: "A floor-length column in heavy crepe, sleeveless, with three soft pleats released from the neckline. The front falls in two long panels over a matching wide trouser, so it reads as a gown and moves like separates.",
		Fabric:      "Oyster crepe over matching trousers",
		Category:    "Evening",
		PriceMinor:  6800000,
		Sizes:       []string{"S", "M", "L"},
		Featured:    true,
		Images:      []Image{{Src: "/static/img/lookbook/02.jpg", Alt: "Zuri column gown in oyster crepe over matching trousers", W: 1333, H: 2000, Tint: "#898c92"}},
	},
	{
		Name:        "Simi Beaded Midi",
		Description: "Short sleeves over a full, gathered skirt, beaded by hand row after row so the stripe never breaks at a seam. Lined in cotton, with a covered zip at the back.",
		Fabric:      "Hand-beaded cotton, cotton lining",
		Category:    "Midi",
		PriceMinor:  2950000,
		Sizes:       []string{"XS", "S", "M", "L", "XL"},
		Images:      []Image{{Src: "/static/img/lookbook/03.jpg", Alt: "Simi beaded midi dress in cobalt and ink blue", W: 1333, H: 2000, Tint: "#797f8e"}},
	},
	{
		Name:        "Ebun Fringed Gown",
		Description: "A plain column under a hand-knotted fringe that runs the full length and pools on the floor. The fringe is worked in two weights — a fine black warp with a violet cord over it — and can be lifted to one shoulder or left to hang.",
		Fabric:      "Plum and black hand-knotted fringe",
		Category:    "Evening",
		PriceMinor:  4200000,
		Sizes:       []string{"S", "M", "L"},
		Images:      []Image{{Src: "/static/img/lookbook/04.jpg", Alt: "Ebun fringed gown in plum and black", W: 1346, H: 2000, Tint: "#8b8e98"}},
	},
	{
		Name:        "Kachi Kaftan",
		Description: "Loose from the shoulder with a slim V and a wide sleeve, cut for heat. The whole ground is embroidered in green and gold, and the front opens over a plain under-dress.",
		Fabric:      "Green and gold embroidered organza",
		Category:    "Kaftan",
		PriceMinor:  2600000,
		Sizes:       []string{"One size", "Plus"},
		Images:      []Image{{Src: "/static/img/lookbook/05.jpg", Alt: "Kachi kaftan in green and gold embroidery", W: 986, H: 2000, Tint: "#b7a49f"}},
	},
	{
		Name:        "Ronke Two-Piece",
		Description: "A draped top over a wide printed trouser gathered at the ankle. Sold as a set and fitted as a set, which is why it takes a week longer than the rest.",
		Fabric:      "Black jersey with printed silk trouser",
		Category:    "Two-piece",
		PriceMinor:  3850000,
		Sizes:       []string{"S", "M", "L"},
		SoldOut:     true,
		Images:      []Image{{Src: "/static/img/lookbook/06.jpg", Alt: "Ronke two-piece: draped black top with printed trouser", W: 1333, H: 2000, Tint: "#7e8088"}},
	},
}

// handleSeedSamples fills an empty shop with the sample collection. It refuses
// to run once there is anything in the catalogue, so it can never overwrite the
// owner's own work.
func (a *App) handleSeedSamples(w http.ResponseWriter, r *http.Request) {
	if !a.simplePost(w, r) {
		return
	}
	token := a.sessionToken(r)
	if len(a.store.Dresses()) > 0 {
		a.sessions.Flash(token, "warn", "Your shop already has dresses, so the sample collection was not added.")
		http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
		return
	}
	for i := len(sampleDresses) - 1; i >= 0; i-- {
		if _, err := a.store.Create(sampleDresses[i]); err != nil {
			slog.Error("seed samples", "error", err)
			a.sessions.Flash(token, "error", "The sample collection could not be added.")
			http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
			return
		}
	}
	a.sessions.Flash(token, "ok",
		"Six sample dresses added. Edit them, or delete them once your own photographs are ready.")
	http.Redirect(w, r, "/admin/dresses", http.StatusSeeOther)
}
