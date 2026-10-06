package main

import (
	"net/http"
	"net/url"
	"strings"
)

// App wires the pieces together and hangs the handlers off them.
type App struct {
	store       *Store
	members     *MemberStore
	mailer      *SMTPMailer
	resetLimit  *Throttle
	verifyLimit *Throttle
	media       *MediaStore
	view        *Renderer
	sessions    *Sessions
	throttle    *Throttle
	credential  Credential
	trustProxy  bool

	// static is set while the shop is being exported to flat files. Pages then
	// render the handful of things a CDN cannot do for them — see export.go.
	static bool
}

// View is the data every page needs: the shop's own settings, the CSP nonce
// for the dynamic style block, and enough context for the header to know
// where it is.
type View struct {
	Title       string
	Description string
	Settings    Settings
	Currency    string
	Nonce       string
	Path        string
	Ordering    bool
	ChatURL     string
	Categories  []string
	Static      bool
	Member      Member
	SignedIn    bool
	CSRF        string
}

// AdminView adds the fields only the admin chrome uses.
type AdminView struct {
	View
	CSRF     string
	Flashes  []Flash
	Warnings []string
	SignedIn bool
}

// newView assembles the shared page data for a request.
func (a *App) newView(r *http.Request, title, description string) View {
	set := a.store.Settings()
	if title == "" {
		title = set.BrandName
	} else {
		title = title + " · " + set.BrandName
	}
	if description == "" {
		description = set.Tagline
	}
	_, csrf, sessionOK := a.currentSession(r)
	member := a.memberFor(r)
	return View{
		Title:       title,
		Description: Excerpt(description, 180),
		Settings:    set,
		Currency:    set.Currency(),
		Nonce:       nonceFrom(r),
		Path:        r.URL.Path,
		Ordering:    set.OrderingEnabled(),
		ChatURL:     ChatURL(set),
		Categories:  a.store.Categories(),
		Static:      a.static,
		Member:      member,
		SignedIn:    member.ID != "",
		CSRF: func() string {
			if sessionOK {
				return csrf
			}
			return ""
		}(),
	}
}

func (a *App) memberFor(r *http.Request) Member {
	if a.members == nil {
		return Member{}
	}
	_, id, ok := a.sessions.Identity(a.sessionToken(r))
	if !ok || id == "" {
		return Member{}
	}
	m, _ := a.members.ByID(id)
	return m
}

// OrderHref is where a lookbook card's order button points. On the live shop
// that is the /order route, which writes the message and redirects; an exported
// site has no route to do that, so the link goes straight to WhatsApp carrying
// the message the server would have composed.
func (v View) OrderHref(d Dress) string {
	if v.Static {
		return OrderURL(v.Settings, d, OrderRequest{})
	}
	return d.Path() + "/order"
}

// CategoryURL is the link to one slice of the lookbook. On the live shop that
// is a query string the home page reads for itself; in an exported site it is a
// page of its own, because a static host cannot read a query string. An empty
// category means the whole lookbook.
func (v View) CategoryURL(category, query string) string {
	if v.Static {
		if category == "" {
			return "/#lookbook"
		}
		return "/c/" + Slugify(category) + "/#lookbook"
	}
	q := url.Values{}
	if category != "" {
		q.Set("c", category)
	}
	if query != "" {
		q.Set("q", query)
	}
	if len(q) == 0 {
		return "/#lookbook"
	}
	return "/?" + q.Encode() + "#lookbook"
}

// errorView is the data behind the storefront's error page.
type errorView struct {
	View
	Status  int
	Heading string
	Message string
}

// showError renders a styled error page rather than net/http's bare text.
func (a *App) showError(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	v := a.newView(r, heading, message)
	a.view.Render(w, status, "error.html", errorView{
		View:    v,
		Status:  status,
		Heading: heading,
		Message: message,
	})
}

// matchesQuery reports whether a dress answers a search box query.
func matchesQuery(d Dress, query string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		d.Name, d.Description, d.Fabric, d.Category, d.Ref,
		strings.Join(d.Sizes, " "),
	}, " "))
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// filterDresses applies the category tab and the search box.
func filterDresses(all []Dress, category, query string) []Dress {
	out := make([]Dress, 0, len(all))
	for _, d := range all {
		if category != "" && !strings.EqualFold(d.Category, category) {
			continue
		}
		if !matchesQuery(d, query) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// heroDress picks the photograph that leads the storefront: whichever piece
// the owner marked as featured, else the newest one with a photograph.
func heroDress(all []Dress) (Dress, bool) {
	for _, d := range all {
		if d.Featured && d.HasImage() {
			return d, true
		}
	}
	for _, d := range all {
		if d.HasImage() {
			return d, true
		}
	}
	return Dress{}, false
}

// relatedDresses picks up to n other pieces to show under a dress, preferring
// the same category.
func relatedDresses(all []Dress, current Dress, n int) []Dress {
	var same, rest []Dress
	for _, d := range all {
		if d.ID == current.ID || !d.HasImage() {
			continue
		}
		if current.Category != "" && strings.EqualFold(d.Category, current.Category) {
			same = append(same, d)
			continue
		}
		rest = append(rest, d)
	}
	out := append(same, rest...)
	if len(out) > n {
		out = out[:n]
	}
	return out
}
