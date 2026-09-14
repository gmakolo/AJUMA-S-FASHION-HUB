package main

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"image/color"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestMain sends the request log nowhere. WithLogging is part of what these
// tests exercise, and without this it writes a line per request over the
// middle of the test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// ------------------------------------------------ the storefront's filtering

// filterFixtures is a small lookbook with a photographed piece, an unphotographed
// one, a featured one, and two categories that differ only in case.
func filterFixtures() []Dress {
	return []Dress{
		{ID: "1", Ref: "AJM-001", Name: "Adaeze Wrap", Category: "Wrap",
			Fabric: "Ankara cotton", Description: "Ties at the waist.",
			Sizes: []string{"S", "M", "XL"}, Images: []Image{{Src: "/media/1.jpg"}}},
		{ID: "2", Ref: "AJM-002", Name: "Zuri Column", Category: "Evening",
			Fabric: "Indigo crepe", Description: "A floor-length column.",
			Featured: true, Images: []Image{{Src: "/media/2.jpg"}}},
		{ID: "3", Ref: "AJM-003", Name: "Simi Midi", Category: "Midi",
			Fabric: "Washed linen", Description: "Puff sleeves, quiet bodice."},
		{ID: "4", Ref: "AJM-004", Name: "Ebun Gown", Category: "evening",
			Fabric: "Plum satin", Description: "Cut on the bias.",
			Images: []Image{{Src: "/media/4.jpg"}}},
	}
}

// ids names a result set compactly, so a wrong selection reads at a glance.
func ids(dresses []Dress) string {
	out := make([]string, len(dresses))
	for i, d := range dresses {
		out[i] = d.ID
	}
	return strings.Join(out, ",")
}

func TestMatchesQuery(t *testing.T) {
	all := filterFixtures()
	tests := []struct {
		name  string
		dress Dress
		query string
		want  bool
	}{
		{"an empty query keeps everything", all[0], "", true},
		{"the case of the query does not matter", all[0], "ADAEZE", true},
		{"every term has to land somewhere", all[0], "ankara wrap", true},
		{"one term that misses is enough to fail", all[0], "ankara evening", false},
		{"the reference is searchable", all[1], "ajm-002", true},
		{"the description is searchable", all[3], "bias", true},
		{"a size is searchable", all[0], "xl", true},
		{"a word from nowhere matches nothing", all[1], "brocade", false},
	}
	for _, tc := range tests {
		if got := matchesQuery(tc.dress, tc.query); got != tc.want {
			t.Errorf("%s: matchesQuery(%q, %q) = %v", tc.name, tc.dress.Name, tc.query, got)
		}
	}
}

func TestFilterDresses(t *testing.T) {
	all := filterFixtures()
	tests := []struct {
		name     string
		category string
		query    string
		want     string
	}{
		{"no filter keeps the order it was given", "", "", "1,2,3,4"},
		{"a category ignores case", "Evening", "", "2,4"},
		{"a category and a query narrow together", "evening", "plum", "4"},
		{"a query alone searches the fabric", "", "linen", "3"},
		{"a category nothing is in comes back empty", "Kaftan", "", ""},
	}
	for _, tc := range tests {
		if got := ids(filterDresses(all, tc.category, tc.query)); got != tc.want {
			t.Errorf("%s: filterDresses(%q, %q) = %q, want %q", tc.name, tc.category, tc.query, got, tc.want)
		}
	}
}

func TestHeroDress(t *testing.T) {
	all := filterFixtures()
	if hero, ok := heroDress(all); !ok || hero.ID != "2" {
		t.Errorf("a featured, photographed dress should lead: got %q, ok=%v", hero.ID, ok)
	}

	// Nothing featured: the newest photographed piece leads instead.
	plain := filterFixtures()
	plain[1].Featured = false
	if hero, ok := heroDress(plain); !ok || hero.ID != "1" {
		t.Errorf("without a house pick the first photograph should lead: got %q, ok=%v", hero.ID, ok)
	}

	// A featured dress with no photograph cannot carry the front page.
	unshot := filterFixtures()
	unshot[1].Featured = false
	unshot[2].Featured = true
	if hero, ok := heroDress(unshot); !ok || hero.ID != "1" {
		t.Errorf("a featured dress with no photograph should be passed over: got %q, ok=%v", hero.ID, ok)
	}

	// A brand new shop has nothing to show.
	var bare []Dress
	for _, d := range filterFixtures() {
		d.Images = nil
		bare = append(bare, d)
	}
	if _, ok := heroDress(bare); ok {
		t.Error("a lookbook with no photographs should report no hero")
	}
	if _, ok := heroDress(nil); ok {
		t.Error("an empty lookbook should report no hero")
	}
}

func TestRelatedDresses(t *testing.T) {
	all := filterFixtures()

	// The same category comes first, then anything else photographed. The
	// dress itself and the unphotographed one are both left out.
	if got := ids(relatedDresses(all, all[1], 3)); got != "4,1" {
		t.Errorf("related to Zuri = %q, want %q", got, "4,1")
	}
	if got := ids(relatedDresses(all, all[1], 1)); got != "4" {
		t.Errorf("related capped at one = %q, want %q", got, "4")
	}
	if got := ids(relatedDresses(all, all[0], 3)); got != "2,4" {
		t.Errorf("related to Adaeze = %q, want %q", got, "2,4")
	}
	if got := ids(relatedDresses(all, Dress{ID: "9"}, 3)); got != "1,2,4" {
		t.Errorf("related to a dress with no category = %q, want %q", got, "1,2,4")
	}
}

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"/admin/settings":                        "/admin/settings",
		"/admin/dresses/new":                     "/admin/dresses/new",
		"":                                       "/admin/dresses",
		"/":                                      "/admin/dresses",
		"/admin/login":                           "/admin/dresses",
		"//evil.example.com":                     "/admin/dresses",
		"/admin//evil.com":                       "/admin/dresses",
		"https://evil.com":                       "/admin/dresses",
		"/admin" + string(rune(92)) + "evil.com": "/admin/dresses",
		"/dress/adaeze/order":                    "/admin/dresses",
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// ------------------------------------------------------ the whole application

const testPassword = "a password long enough to pass"

// testCred is the studio password hashed at a fraction of the real work
// factor. 600,000 PBKDF2 iterations is the right cost for a live shop and far
// too slow to sign in over and over across a test run; auth_test.go covers the
// real hashing path at its real cost.
var testCred = sync.OnceValue(func() Credential {
	salt := []byte("a fixed salt, for tests only")
	key, err := pbkdf2.Key(sha256.New, testPassword, salt, 1000, pbkdf2KeyLen)
	if err != nil {
		panic(err)
	}
	return Credential{iterations: 1000, salt: salt, key: key}
})

// shop is a running copy of the application — the same routes, templates,
// middleware and store that main() wires together, over a temporary directory.
// The client keeps cookies, so the admin tests sign in the way a browser does.
type shop struct {
	t      *testing.T
	app    *App
	client *http.Client
	base   string
}

func newShop(t *testing.T) *shop {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	media, err := OpenMediaStore(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	view, err := NewRenderer(embedded, false)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	static, err := StaticHandler(embedded)
	if err != nil {
		t.Fatalf("static handler: %v", err)
	}
	app := &App{
		store:      store,
		media:      media,
		view:       view,
		sessions:   NewSessions(),
		throttle:   NewThrottle(),
		credential: testCred(),
	}
	mux := http.NewServeMux()
	app.routes(mux, static)
	srv := httptest.NewServer(WithRecovery(WithLogging(WithSecurityHeaders(mux))))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &shop{t: t, app: app, base: srv.URL, client: &http.Client{
		Jar: jar,
		// Every redirect here is part of what is being tested, so none of
		// them are followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// send makes a request and reads the whole body, which every test wants.
func (s *shop) send(req *http.Request) (*http.Response, string) {
	s.t.Helper()
	res, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		s.t.Fatalf("read %s %s: %v", req.Method, req.URL.Path, err)
	}
	return res, string(body)
}

// do makes one request; a nil form means there is no body.
func (s *shop) do(method, path string, form url.Values, headers map[string]string) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, s.base+path, body)
	if err != nil {
		s.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return s.send(req)
}

func (s *shop) get(path string) (*http.Response, string) {
	s.t.Helper()
	return s.do(http.MethodGet, path, nil, nil)
}

// post sends a small form, adding the session's CSRF token unless the test
// has deliberately supplied its own.
func (s *shop) post(path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("csrf") == "" {
		form.Set("csrf", s.csrf())
	}
	return s.do(http.MethodPost, path, form, nil)
}

// photograph is one file part in a submission to the dress form.
type photograph struct {
	name    string
	content []byte
}

// postDress submits the add or edit form the way a browser does: multipart,
// because that form carries file inputs.
func (s *shop) postDress(path string, fields url.Values, photos ...photograph) (*http.Response, string) {
	s.t.Helper()
	fields.Set("csrf", s.csrf())
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, value := range values {
			if err := form.WriteField(name, value); err != nil {
				s.t.Fatalf("write field %s: %v", name, err)
			}
		}
	}
	for _, p := range photos {
		part, err := form.CreateFormFile("photos", p.name)
		if err != nil {
			s.t.Fatalf("attach %s: %v", p.name, err)
		}
		if _, err := part.Write(p.content); err != nil {
			s.t.Fatalf("write %s: %v", p.name, err)
		}
	}
	if err := form.Close(); err != nil {
		s.t.Fatalf("close the multipart form: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, s.base+path, &body)
	if err != nil {
		s.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	return s.send(req)
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// csrf lifts the token out of a rendered form, which doubles as a check that
// the admin templates still carry the hidden field at all.
func (s *shop) csrf() string {
	s.t.Helper()
	res, body := s.get("/admin/settings")
	if res.StatusCode != http.StatusOK {
		s.t.Fatalf("reading a CSRF token from the settings page: status %d", res.StatusCode)
	}
	found := csrfField.FindStringSubmatch(body)
	if found == nil {
		s.t.Fatal("the settings form carries no hidden csrf field")
	}
	return found[1]
}

// signIn walks the real login form and keeps the cookie it hands back.
func (s *shop) signIn() {
	s.t.Helper()
	res, _ := s.do(http.MethodPost, "/admin/login", url.Values{"password": {testPassword}}, nil)
	s.wants(res, http.StatusSeeOther)
}

// seed fills an empty shop with the sample collection, through the button the
// owner would press.
func (s *shop) seed() {
	s.t.Helper()
	res, _ := s.post("/admin/sample", nil)
	s.wants(res, http.StatusSeeOther)
	if n := len(s.app.store.Dresses()); n != len(sampleDresses) {
		s.t.Fatalf("seeded %d dresses, want %d", n, len(sampleDresses))
	}
}

// enableOrdering records a WhatsApp number and a public address, which is what
// switches every order button on.
func (s *shop) enableOrdering() {
	s.t.Helper()
	set := s.app.store.Settings()
	set.WhatsApp = "2348155604988"
	set.BaseURL = "https://ajumafashionhub.com"
	if err := s.app.store.SaveSettings(set); err != nil {
		s.t.Fatalf("save settings: %v", err)
	}
}

// wants fails the test unless the response carries the status asked for.
func (s *shop) wants(res *http.Response, status int) {
	s.t.Helper()
	if res.StatusCode != status {
		s.t.Fatalf("%s %s: status %d, want %d",
			res.Request.Method, res.Request.URL.Path, res.StatusCode, status)
	}
}

// mentions fails unless every fragment appears in the page.
func mentions(t *testing.T, what, body string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(body, fragment) {
			t.Errorf("%s does not mention %q", what, fragment)
		}
	}
}

// orderText decodes the message out of a wa.me redirect, so a test can read
// what the buyer would be about to send.
func orderText(t *testing.T, location string) string {
	t.Helper()
	if !strings.HasPrefix(location, "https://wa.me/") {
		t.Fatalf("order redirect went to %q, not to wa.me", location)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse %q: %v", location, err)
	}
	return parsed.Query().Get("text")
}

// stock puts the sample collection straight into the store, in the order the
// seed button uses, for the tests that are about the storefront rather than
// about the studio.
func (s *shop) stock() {
	s.t.Helper()
	for i := len(sampleDresses) - 1; i >= 0; i-- {
		if _, err := s.app.store.Create(sampleDresses[i]); err != nil {
			s.t.Fatalf("stock the lookbook: %v", err)
		}
	}
}

// dress reads one stored dress back, so an assertion can use the reference and
// the price the store actually allocated.
func (s *shop) dress(slug string) Dress {
	s.t.Helper()
	d, ok := s.app.store.BySlug(slug)
	if !ok {
		s.t.Fatalf("no dress at /dress/%s", slug)
	}
	return d
}

// ---------------------------------------------------------- the storefront

func TestHomePageShowsTheLookbook(t *testing.T) {
	s := newShop(t)
	s.stock()

	res, body := s.get("/")
	s.wants(res, http.StatusOK)
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	ojoma := s.dress("ojoma-achi-set")
	mentions(t, "the front page", body,
		"Ajuma Fashion Hub",
		"Ojoma Achi Set",
		"Ronke Two-Piece",
		"Sold out",
		FormatMoney("₦", ojoma.PriceMinor),
		`href="/dress/ojoma-achi-set"`,
		"/static/img/lookbook/01.jpg",
		"6 pieces",
	)
	// Without a number the order buttons have to stay off.
	if strings.Contains(body, "wa.me") {
		t.Error("the front page offers WhatsApp before a number has been set")
	}
}

func TestHomePageFiltersTheLookbook(t *testing.T) {
	s := newShop(t)
	s.stock()

	_, body := s.get("/?c=Kaftan")
	mentions(t, "the Kaftan tab", body, "Kachi Kaftan")
	if strings.Contains(body, "Ronke Two-Piece") {
		t.Error("the Kaftan tab is showing a two-piece")
	}

	_, body = s.get("/?q=oyster")
	mentions(t, "a search for oyster", body, "Zuri Column Gown")
	if strings.Contains(body, "Kachi Kaftan") {
		t.Error("a search for oyster returned the kaftan")
	}

	_, body = s.get("/?q=brocade")
	mentions(t, "a search that finds nothing", body, "Nothing matches", "Show everything")
}

func TestDressPageShowsTheOrderForm(t *testing.T) {
	s := newShop(t)
	s.stock()
	s.enableOrdering()
	ojoma := s.dress("ojoma-achi-set")

	res, body := s.get("/dress/ojoma-achi-set")
	s.wants(res, http.StatusOK)
	mentions(t, "the dress page", body,
		"Ojoma Achi Set",
		FormatMoney("₦", ojoma.PriceMinor),
		ojoma.Ref,
		`action="/dress/ojoma-achi-set/order"`,
		`name="size" value="S"`,
		`name="qty"`,
		`name="notes"`,
		`id="wa-preview"`,
		"Order on WhatsApp",
		// The related strip carries the rest of the lookbook.
		`href="/dress/zuri-column-gown"`,
	)
}

func TestDressPageWithoutANumberHidesTheButton(t *testing.T) {
	s := newShop(t)
	s.stock()

	res, body := s.get("/dress/kachi-kaftan")
	s.wants(res, http.StatusOK)
	mentions(t, "the dress page", body, "Ordering is switched off until the shop adds its WhatsApp number.")
}

func TestOrderRedirectsToWhatsApp(t *testing.T) {
	s := newShop(t)
	s.stock()
	s.enableOrdering()
	ojoma := s.dress("ojoma-achi-set")

	res, _ := s.get("/dress/ojoma-achi-set/order")
	s.wants(res, http.StatusSeeOther)
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q; the redirect carries the buyer's own words, so it must not be cached", cc)
	}
	location := res.Header.Get("Location")
	if !strings.HasPrefix(location, "https://wa.me/2348155604988?text=") {
		t.Fatalf("Location = %q, want a wa.me link to the shop's number", location)
	}
	text := orderText(t, location)
	mentions(t, "the WhatsApp message", text,
		"Ajuma Fashion Hub",
		"Ojoma Achi Set",
		FormatMoney("₦", ojoma.PriceMinor),
		ojoma.Ref,
		// The photograph travels as a link — a deep link cannot carry a file.
		"https://ajumafashionhub.com/static/img/lookbook/01.jpg",
		"https://ajumafashionhub.com/dress/ojoma-achi-set",
	)
	if strings.ContainsAny(text, "{}") {
		t.Errorf("a placeholder was left unfilled in %q", text)
	}
}

func TestOrderPostCarriesTheBuyersChoices(t *testing.T) {
	s := newShop(t)
	s.stock()
	s.enableOrdering()

	res, _ := s.do(http.MethodPost, "/dress/ojoma-achi-set/order", url.Values{
		"size":  {"L"},
		"qty":   {"2"},
		"notes": {"Sleeves to the elbow, needed by the 20th"},
	}, nil)
	s.wants(res, http.StatusSeeOther)

	text := orderText(t, res.Header.Get("Location"))
	mentions(t, "the WhatsApp message", text,
		"Size: L", "Quantity: 2", "Note: Sleeves to the elbow, needed by the 20th")
}

func TestOrderForASoldOutPieceStillAsks(t *testing.T) {
	s := newShop(t)
	s.stock()
	s.enableOrdering()

	res, _ := s.get("/dress/ronke-two-piece/order")
	s.wants(res, http.StatusSeeOther)
	mentions(t, "the message for a sold-out piece", orderText(t, res.Header.Get("Location")), soldOutNote)
}

func TestOrderWithoutANumberIsUnavailable(t *testing.T) {
	s := newShop(t)
	s.stock()

	res, body := s.get("/dress/ojoma-achi-set/order")
	s.wants(res, http.StatusServiceUnavailable)
	mentions(t, "the page shown instead", body, "Ordering opens shortly")
}

func TestMissingPagesAreNotFound(t *testing.T) {
	s := newShop(t)
	s.stock()

	for _, path := range []string{
		"/dress/not-a-dress",
		"/dress/not-a-dress/order",
		"/nowhere",
		"/static/css/nothing.css",
	} {
		res, _ := s.get(path)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, res.StatusCode)
		}
	}
}

func TestRobotsAndSitemap(t *testing.T) {
	s := newShop(t)
	s.stock()

	res, body := s.get("/robots.txt")
	s.wants(res, http.StatusOK)
	mentions(t, "robots.txt", body, "Disallow: /admin", "Disallow: /order", "Sitemap: http")

	res, body = s.get("/sitemap.xml")
	s.wants(res, http.StatusOK)
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	mentions(t, "the sitemap", body,
		"<urlset", "/dress/ojoma-achi-set</loc>", "/dress/ronke-two-piece</loc>")
	if n, want := strings.Count(body, "<url>"), len(sampleDresses)+1; n != want {
		t.Errorf("the sitemap lists %d urls, want %d (the front page and every dress)", n, want)
	}
}

func TestHealthAndStaticAssets(t *testing.T) {
	s := newShop(t)

	res, body := s.get("/healthz")
	s.wants(res, http.StatusOK)
	if strings.TrimSpace(body) != "ok" {
		t.Errorf("/healthz said %q, want ok", strings.TrimSpace(body))
	}

	res, body = s.get("/static/css/site.css")
	s.wants(res, http.StatusOK)
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("the stylesheet came back as %q, want text/css", ct)
	}
	if len(body) < 1000 {
		t.Errorf("the stylesheet is only %d bytes, so something is wrong with the embed", len(body))
	}

	res, _ = s.get("/static/img/lookbook/01.jpg")
	s.wants(res, http.StatusOK)
}

var cspNonce = regexp.MustCompile(`'nonce-([^']+)'`)

func TestSecurityHeadersAndNonce(t *testing.T) {
	s := newShop(t)

	res, body := s.get("/")
	s.wants(res, http.StatusOK)
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := res.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	csp := res.Header.Get("Content-Security-Policy")
	mentions(t, "the content security policy", csp,
		"default-src 'self'", "object-src 'none'", "form-action 'self' https://wa.me")

	found := cspNonce.FindStringSubmatch(csp)
	if found == nil {
		t.Fatalf("no nonce in the policy: %q", csp)
	}
	// The stylesheet link the page loads has to carry the nonce the header
	// minted for this very request, or the browser will refuse it.
	if !strings.Contains(body, `nonce="`+found[1]+`"`) {
		t.Errorf("the page does not carry the request's nonce %q", found[1])
	}
}

// ------------------------------------------------------------- the studio

// settingsForm is a complete, valid settings submission. Individual tests
// change the one field they are about.
func settingsForm() url.Values {
	return url.Values{
		"brand_name":       {"Ajuma Fashion Hub"},
		"tagline":          {"Dresses cut, sewn and finished by hand"},
		"story":            {"A one-woman atelier. Every dress is cut and sewn here."},
		"announcement":     {"Free delivery in Abuja this month"},
		"whatsapp":         {"234 815 560 4988"},
		"base_url":         {"ajumafashionhub.com"},
		"message_template": {DefaultMessageTemplate},
		"currency_symbol":  {"₦"},
		"location":         {"Abuja, Nigeria"},
		"delivery_note":    {"Nationwide delivery, 2–5 days after the dress is finished"},
		"email":            {"hello@ajumafashionhub.com"},
		"instagram":        {"@ajumafashionhub"},
		"tiktok":           {"@ajumafashionhub"},
	}
}

func TestStudioIsLockedUntilSignIn(t *testing.T) {
	s := newShop(t)

	// A page asks for a sign-in and remembers where the owner was going.
	res, _ := s.get("/admin/dresses")
	s.wants(res, http.StatusSeeOther)
	if loc := res.Header.Get("Location"); loc != "/admin/login?next=%2Fadmin%2Fdresses" {
		t.Errorf("Location = %q, want the login page with next=/admin/dresses", loc)
	}
	res, _ = s.get("/admin")
	s.wants(res, http.StatusSeeOther)
	if loc := res.Header.Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}

	// A change, on the other hand, is simply refused.
	res, body := s.do(http.MethodPost, "/admin/settings", settingsForm(), nil)
	s.wants(res, http.StatusForbidden)
	mentions(t, "the refusal", body, "Your session has ended")
	if s.app.store.Settings().WhatsApp != "" {
		t.Error("a signed-out POST changed the shop's settings")
	}
}

func TestSignInAndOut(t *testing.T) {
	s := newShop(t)

	res, body := s.do(http.MethodPost, "/admin/login", url.Values{"password": {"not it"}}, nil)
	s.wants(res, http.StatusUnauthorized)
	mentions(t, "the login page", body, "That password is not right.")

	res, _ = s.do(http.MethodPost, "/admin/login",
		url.Values{"password": {testPassword}, "next": {"/admin/settings"}}, nil)
	s.wants(res, http.StatusSeeOther)
	if loc := res.Header.Get("Location"); loc != "/admin/settings" {
		t.Errorf("Location = %q, want the page the owner was heading for", loc)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("signing in set no session cookie")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != sessionPath {
		t.Errorf("session cookie = %+v; want HttpOnly, SameSite=Lax, Path=%s", cookie, sessionPath)
	}

	res, body = s.get("/admin/dresses")
	s.wants(res, http.StatusOK)
	mentions(t, "the studio", body, "Lookbook", "Load the samples", `name="csrf"`)

	res, _ = s.post("/admin/logout", nil)
	s.wants(res, http.StatusSeeOther)
	res, _ = s.get("/admin/dresses")
	s.wants(res, http.StatusSeeOther)
}

func TestPostsFromElsewhereAreRefused(t *testing.T) {
	s := newShop(t)

	// Fetch metadata says the request came from another site.
	res, body := s.do(http.MethodPost, "/admin/login", url.Values{"password": {testPassword}},
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	s.wants(res, http.StatusForbidden)
	mentions(t, "the refusal", body, "did not come from this site")

	// So does the Origin header, for a browser that sends no Fetch metadata.
	res, _ = s.do(http.MethodPost, "/admin/login", url.Values{"password": {testPassword}},
		map[string]string{"Origin": "https://evil.example.com"})
	s.wants(res, http.StatusForbidden)

	// And a signed-in session still needs the token from its own form.
	s.signIn()
	res, body = s.do(http.MethodPost, "/admin/settings",
		withCSRF(settingsForm(), "not-the-token"), nil)
	s.wants(res, http.StatusForbidden)
	mentions(t, "the refusal", body, "security token did not match")
	if s.app.store.Settings().WhatsApp != "" {
		t.Error("a POST with the wrong token changed the shop's settings")
	}
}

// withCSRF puts a chosen token on a form, for the tests that send a bad one.
func withCSRF(form url.Values, token string) url.Values {
	form.Set("csrf", token)
	return form
}

func TestStudioRunsTheShop(t *testing.T) {
	s := newShop(t)
	s.signIn()

	// The sample collection, through the button an empty lookbook offers.
	s.seed()
	_, body := s.get("/admin/dresses")
	mentions(t, "the studio", body,
		"Six sample dresses added.", "Ojoma Achi Set", "Ronke Two-Piece")

	// Pressing it again cannot overwrite the owner's own work.
	res, _ := s.post("/admin/sample", nil)
	s.wants(res, http.StatusSeeOther)
	if n := len(s.app.store.Dresses()); n != len(sampleDresses) {
		t.Errorf("a second seeding left %d dresses, want %d", n, len(sampleDresses))
	}
	_, body = s.get("/admin/dresses")
	mentions(t, "the studio", body, "already has dresses")

	// Reordering the lookbook.
	second := s.app.store.Dresses()[1]
	res, _ = s.post("/admin/dresses/"+second.ID+"/move", url.Values{"dir": {"up"}})
	s.wants(res, http.StatusSeeOther)
	if lead := s.app.store.Dresses()[0]; lead.ID != second.ID {
		t.Errorf("after moving up, the lookbook leads with %q, want %q", lead.Name, second.Name)
	}

	// The number, which is the thing that switches ordering on.
	res, _ = s.post("/admin/settings", settingsForm())
	s.wants(res, http.StatusSeeOther)
	set := s.app.store.Settings()
	if set.WhatsApp != "2348155604988" {
		t.Errorf("WhatsApp = %q, want the typed number reduced to digits", set.WhatsApp)
	}
	if set.BaseURL != "https://ajumafashionhub.com" {
		t.Errorf("BaseURL = %q, want a scheme filled in", set.BaseURL)
	}
	if set.Instagram != "@ajumafashionhub" || set.Location != "Abuja, Nigeria" {
		t.Errorf("settings did not keep the contact fields: %+v", set)
	}

	// ...so the storefront now carries the announcement and the buttons work.
	_, body = s.get("/")
	mentions(t, "the front page", body,
		"Free delivery in Abuja this month", `href="/dress/ojoma-achi-set/order"`)
	res, _ = s.get("/dress/ojoma-achi-set/order")
	s.wants(res, http.StatusSeeOther)
	mentions(t, "the order redirect", res.Header.Get("Location"), "https://wa.me/2348155604988")
}

func TestSettingsRefusesWhatItCannotUse(t *testing.T) {
	s := newShop(t)
	s.signIn()

	form := settingsForm()
	form.Set("whatsapp", "call me")
	res, body := s.post("/admin/settings", form)
	s.wants(res, http.StatusUnprocessableEntity)
	mentions(t, "the settings page", body, "Nothing was saved.", "does not look like a phone number")
	if s.app.store.Settings().WhatsApp != "" {
		t.Error("a rejected form still changed the number")
	}

	// A local number is refused rather than guessed at.
	form = settingsForm()
	form.Set("whatsapp", "0815 560 4988")
	res, body = s.post("/admin/settings", form)
	s.wants(res, http.StatusUnprocessableEntity)
	mentions(t, "the settings page", body, "replace the leading 0 with your country code")

	// And the shop cannot lose its name.
	form = settingsForm()
	form.Set("brand_name", "")
	res, body = s.post("/admin/settings", form)
	s.wants(res, http.StatusUnprocessableEntity)
	mentions(t, "the settings page", body, "The shop needs a name.")
	if s.app.store.Settings().BrandName != "Ajuma Fashion Hub" {
		t.Error("a rejected form still changed the shop's name")
	}

	// An empty template goes back to the wording the shop came with.
	form = settingsForm()
	form.Set("message_template", "   ")
	res, _ = s.post("/admin/settings", form)
	s.wants(res, http.StatusSeeOther)
	if s.app.store.Settings().MessageTemplate != DefaultMessageTemplate {
		t.Error("clearing the template did not restore the default wording")
	}
}

func TestADressGoesUpIsEditedAndComesDown(t *testing.T) {
	s := newShop(t)
	s.signIn()
	s.enableOrdering()

	res, _ := s.postDress("/admin/dresses/new", url.Values{
		"name":        {"Amaka Bubu Dress"},
		"description": {"A loose bubu in soft adire, gathered at the yoke and left to fall."},
		"fabric":      {"Hand-dyed adire"},
		"category":    {"Bubu"},
		"price":       {"28,500"},
		"sizes":       {"S, M, L"},
		"featured":    {"1"},
	})
	s.wants(res, http.StatusSeeOther)

	d := s.dress("amaka-bubu-dress")
	if d.PriceMinor != 2850000 {
		t.Errorf("price stored as %d minor units, want 2850000", d.PriceMinor)
	}
	if got := strings.Join(d.Sizes, "/"); got != "S/M/L" {
		t.Errorf("sizes = %q, want S/M/L", got)
	}
	if !d.Featured || d.SoldOut || d.Ref == "" {
		t.Errorf("featured=%v soldOut=%v ref=%q, want true, false and a reference",
			d.Featured, d.SoldOut, d.Ref)
	}

	// It is on the front page at once, priced, and the studio says the one
	// thing still missing.
	_, body := s.get("/")
	mentions(t, "the front page", body, "Amaka Bubu Dress", FormatMoney("₦", 2850000))
	_, body = s.get("/admin/dresses")
	mentions(t, "the studio", body, "has no photograph yet")

	// An edit keeps the reference and the slug, and changes what it was told to.
	res, _ = s.postDress("/admin/dresses/"+d.ID, url.Values{
		"name":        {"Amaka Bubu Dress"},
		"description": {d.Description},
		"fabric":      {d.Fabric},
		"category":    {d.Category},
		"price":       {"31,000"},
		"sizes":       {"S, M, L, XL"},
		"sold_out":    {"1"},
	})
	s.wants(res, http.StatusSeeOther)

	edited := s.dress("amaka-bubu-dress")
	if edited.Ref != d.Ref || edited.ID != d.ID {
		t.Errorf("editing changed the identity: ref %q -> %q", d.Ref, edited.Ref)
	}
	if edited.PriceMinor != 3100000 || !edited.SoldOut || len(edited.Sizes) != 4 {
		t.Errorf("the edit did not take: %+v", edited)
	}

	// Sold out, so the page asks rather than offering the button.
	_, body = s.get("/dress/amaka-bubu-dress")
	mentions(t, "the dress page", body, "Sold out.", "it can usually be made again")

	// And it comes down again.
	res, _ = s.post("/admin/dresses/"+d.ID+"/delete", nil)
	s.wants(res, http.StatusSeeOther)
	if _, ok := s.app.store.BySlug("amaka-bubu-dress"); ok {
		t.Fatal("the dress is still in the lookbook after being deleted")
	}
	res, _ = s.get("/dress/amaka-bubu-dress")
	s.wants(res, http.StatusNotFound)
}

func TestDressFormHandsBackWhatWasTyped(t *testing.T) {
	s := newShop(t)
	s.signIn()

	res, body := s.postDress("/admin/dresses/new", url.Values{
		"name":        {""},
		"description": {"A loose bubu in soft adire."},
		"price":       {"free"},
		"sizes":       {"S, M"},
	})
	s.wants(res, http.StatusUnprocessableEntity)
	mentions(t, "the form", body,
		"Give the dress a name.",
		"Enter a price, for example 28500.",
		// Nothing the owner typed is thrown away.
		"A loose bubu in soft adire.",
		`value="free"`,
		`value="S, M"`,
	)
	if n := len(s.app.store.Dresses()); n != 0 {
		t.Errorf("a rejected form still added %d dresses", n)
	}
}

func TestEditingADressThatIsGone(t *testing.T) {
	s := newShop(t)
	s.signIn()

	res, _ := s.get("/admin/dresses/nope")
	s.wants(res, http.StatusNotFound)
	res, _ = s.post("/admin/dresses/nope/delete", nil)
	s.wants(res, http.StatusNotFound)
}

func TestPhotographsAreUploadedPromotedAndRemoved(t *testing.T) {
	s := newShop(t)
	s.signIn()

	res, _ := s.postDress("/admin/dresses/new", url.Values{
		"name":        {"Nkechi Two-Piece"},
		"price":       {"42,000"},
		"category":    {"Two-piece"},
		"sizes":       {"S, M"},
		"description": {"Cotton, with a hand-finished hem."},
	},
		photograph{"front.png", pngBytes(t, 60, 90, color.RGBA{R: 0x2E, G: 0x4A, B: 0x3C, A: 0xFF})},
		photograph{"back.PNG", pngBytes(t, 60, 90, color.RGBA{R: 0xC8, G: 0xA2, B: 0x6A, A: 0xFF})})
	s.wants(res, http.StatusSeeOther)

	d := s.dress("nkechi-two-piece")
	if len(d.Images) != 2 {
		t.Fatalf("the dress has %d photographs, want 2", len(d.Images))
	}
	front, back := d.Images[0], d.Images[1]
	for _, img := range d.Images {
		if !strings.HasPrefix(img.Src, mediaURLPrefix) || img.W != 60 || img.H != 90 || img.Tint == "" {
			t.Errorf("photograph %+v was not processed", img)
		}
		if _, err := os.Stat(filepath.Join(s.app.media.Dir(), strings.TrimPrefix(img.Src, mediaURLPrefix))); err != nil {
			t.Errorf("%s is not on disk: %v", img.Src, err)
		}
	}
	if front.Tint == back.Tint {
		t.Error("both photographs came out the same colour")
	}

	// The edit form offers the cover, the other photograph, and the price in a
	// form that can be typed over.
	res, body := s.get("/admin/dresses/" + d.ID)
	s.wants(res, http.StatusOK)
	mentions(t, "the edit form", body, front.Src, back.Src, "Cover", "Make cover",
		`value="42000"`, `value="S, M"`, `name="photos"`)

	// Promote the second photograph to the cover.
	res, _ = s.post("/admin/dresses/"+d.ID+"/photo", url.Values{
		"src": {back.Src}, "action": {"primary"},
	})
	s.wants(res, http.StatusSeeOther)
	if got := s.dress("nkechi-two-piece").Images; got[0].Src != back.Src || got[1].Src != front.Src {
		t.Errorf("the cover is %s and then %s, want %s first", got[0].Src, got[1].Src, back.Src)
	}
	res, body = s.get("/admin/dresses/" + d.ID)
	s.wants(res, http.StatusOK)
	mentions(t, "the edit form", body, "Cover photograph changed.")

	// A src that is not on this dress changes nothing.
	res, _ = s.post("/admin/dresses/"+d.ID+"/photo", url.Values{
		"src": {"/media/img_somewhere_else.jpg"}, "action": {"remove"},
	})
	s.wants(res, http.StatusSeeOther)
	if len(s.dress("nkechi-two-piece").Images) != 2 {
		t.Error("a photograph was removed by a src that does not belong to the dress")
	}

	// Remove the one that is no longer the cover.
	res, _ = s.post("/admin/dresses/"+d.ID+"/photo", url.Values{
		"src": {front.Src}, "action": {"remove"},
	})
	s.wants(res, http.StatusSeeOther)
	left := s.dress("nkechi-two-piece").Images
	if len(left) != 1 || left[0].Src != back.Src {
		t.Fatalf("the dress is left with %+v, want only the cover", left)
	}
	gone := filepath.Join(s.app.media.Dir(), strings.TrimPrefix(front.Src, mediaURLPrefix))
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("%s is still on disk after being removed", front.Src)
	}
	res, body = s.get("/admin/dresses/" + d.ID)
	s.wants(res, http.StatusOK)
	mentions(t, "the edit form", body, "Photograph removed.")

	// Deleting the dress takes its last photograph with it.
	res, _ = s.post("/admin/dresses/"+d.ID+"/delete", nil)
	s.wants(res, http.StatusSeeOther)
	last := filepath.Join(s.app.media.Dir(), strings.TrimPrefix(back.Src, mediaURLPrefix))
	if _, err := os.Stat(last); !os.IsNotExist(err) {
		t.Errorf("%s outlived the dress it belonged to", back.Src)
	}
}

func TestTheEmptyFormsAreOffered(t *testing.T) {
	s := newShop(t)
	s.signIn()

	res, body := s.get("/admin/dresses/new")
	s.wants(res, http.StatusOK)
	mentions(t, "the blank form", body, "Add a dress", `action="/admin/dresses/new"`,
		`name="name"`, `name="price"`, `name="photos"`, `value="S, M, L, XL"`)
	if strings.Contains(body, "Photographs on this dress") {
		t.Error("the blank form offers photograph actions for a dress that does not exist yet")
	}

	// The lookbook is empty until something is added, and says so.
	res, body = s.get("/admin/dresses")
	s.wants(res, http.StatusOK)
	mentions(t, "the empty lookbook", body, "Load the samples")
}

func TestTheSignInPageIsOfferedOnceAndThenStepsAside(t *testing.T) {
	s := newShop(t)

	res, body := s.get("/admin/login?next=%2Fadmin%2Fsettings")
	s.wants(res, http.StatusOK)
	mentions(t, "the sign-in page", body, `name="password"`, `type="password"`,
		`name="next"`, `value="/admin/settings"`, "Sign in to the studio")
	// There is no session yet to hold a token, so this one form is guarded by
	// where the request came from rather than by a CSRF field.
	if strings.Contains(body, `name="csrf"`) {
		t.Error("the sign-in form carries a CSRF token, which cannot mean anything before a session exists")
	}
	if strings.Contains(body, "Sign out") {
		t.Error("the sign-in page offers a way to sign out")
	}

	// Somewhere else entirely is not carried through as a destination.
	res, body = s.get("/admin/login?next=https%3A%2F%2Felsewhere.example%2Ftake-over")
	s.wants(res, http.StatusOK)
	if strings.Contains(body, "elsewhere.example") {
		t.Error("the sign-in form would send the owner off to another site")
	}

	// Once signed in, the sign-in page is out of the way.
	s.signIn()
	res, _ = s.get("/admin/login")
	s.wants(res, http.StatusSeeOther)
	if got := res.Header.Get("Location"); got != "/admin/dresses" {
		t.Errorf("Location = %q, want /admin/dresses", got)
	}
	res, _ = s.get("/admin")
	s.wants(res, http.StatusSeeOther)
	if got := res.Header.Get("Location"); got != "/admin/dresses" {
		t.Errorf("a signed-in /admin went to %q, want /admin/dresses", got)
	}
}

func TestPriceInput(t *testing.T) {
	// The reverse of ParsePrice: what the owner sees in the box when they come
	// back to edit a dress.
	cases := []struct {
		minor int64
		want  string
	}{
		{2850000, "28500"},
		{2850050, "28500.50"},
		{100, "1"},
		{5, "0.05"},
		{0, ""},
		{-100, ""},
	}
	for _, c := range cases {
		if got := priceInput(c.minor); got != c.want {
			t.Errorf("priceInput(%d) = %q, want %q", c.minor, got, c.want)
		}
	}
}

func TestOriginFollowsTheSettingsThenTheRequest(t *testing.T) {
	s := newShop(t)
	req := httptest.NewRequest(http.MethodGet, "http://shop.example/robots.txt", nil)

	if got := s.app.origin(req); got != "http://shop.example" {
		t.Errorf("origin = %q, want the address the request came in on", got)
	}

	// Headers from a proxy are only believed when the shop was told to trust one.
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "ajumafashionhub.com")
	if got := s.app.origin(req); got != "http://shop.example" {
		t.Errorf("origin = %q, want proxy headers ignored by default", got)
	}
	s.app.trustProxy = true
	if got := s.app.origin(req); got != "https://ajumafashionhub.com" {
		t.Errorf("origin behind a trusted proxy = %q, want https://ajumafashionhub.com", got)
	}
	s.app.trustProxy = false

	// What the owner recorded in settings wins over both, without its slash.
	set := s.app.store.Settings()
	set.BaseURL = "https://ajumafashionhub.com/"
	if err := s.app.store.SaveSettings(set); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if got := s.app.origin(req); got != "https://ajumafashionhub.com" {
		t.Errorf("origin = %q, want what the owner recorded", got)
	}
}
