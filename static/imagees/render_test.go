package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// call invokes one of the template functions by name, the way a template does.
func call(t *testing.T, name string, args ...any) any {
	t.Helper()
	fn, ok := templateFuncs[name]
	if !ok {
		t.Fatalf("there is no template function called %q", name)
	}
	value := reflect.ValueOf(fn)
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		in[i] = reflect.ValueOf(a)
	}
	out := value.Call(in)
	if len(out) == 2 && !out[1].IsNil() {
		t.Fatalf("%s%v returned an error: %v", name, args, out[1].Interface())
	}
	return out[0].Interface()
}

func TestParagraphsFunc(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"a blank line starts a new paragraph", "Cut by hand.\n\nFinished by hand.",
			[]string{"Cut by hand.", "Finished by hand."}},
		{"a single line is one paragraph", "  Cut by hand.  ", []string{"Cut by hand."}},
		{"windows line endings", "One\r\n\r\nTwo", []string{"One", "Two"}},
		{"a run of blank lines does not make empty paragraphs", "One\n\n\n\nTwo", []string{"One", "Two"}},
		{"a line break inside a paragraph is kept", "One\nstill one", []string{"One\nstill one"}},
		{"nothing at all", "   \n\n  ", nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		got := call(t, "paragraphs", c.in).([]string)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: paragraphs(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestTintFunc(t *testing.T) {
	// A tint is read out of the catalogue and written straight into a style
	// attribute, so anything that is not a colour must not reach the page.
	const fallback = "#e8e0d5"
	cases := []struct{ in, want string }{
		{"#5d3a4e", "#5d3a4e"},
		{"#FFF", "#FFF"},
		{"#abc123", "#abc123"},
		{"", fallback},
		{"red", fallback},
		{"#12", fallback},
		{"#1234", fallback},
		{"#12345g", fallback},
		{" #123456 ", fallback},
		{"#123456;background:url(http://elsewhere/x)", fallback},
		{"url(javascript:alert(1))", fallback},
	}
	for _, c := range cases {
		if got := call(t, "tint", c.in).(template.CSS); string(got) != c.want {
			t.Errorf("tint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSeqAndAddFuncs(t *testing.T) {
	if got, want := call(t, "seq", 3).([]int), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("seq(3) = %v, want %v", got, want)
	}
	if got := call(t, "seq", 0).([]int); len(got) != 0 {
		t.Errorf("seq(0) = %v, want nothing", got)
	}
	if got := call(t, "seq", -5).([]int); len(got) != 0 {
		t.Errorf("seq(-5) = %v, want nothing", got)
	}
	if got := call(t, "add", 0, 1).(int); got != 1 {
		t.Errorf("add(0, 1) = %d, want 1 — photographs are counted from one", got)
	}
}

func TestAmountFunc(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{2850000, "28500.00"},
		{2850050, "28500.50"},
		{5, "0.05"},
		{99, "0.99"},
		{100, "1.00"},
		{0, "0.00"},
	}
	for _, c := range cases {
		if got := call(t, "amount", c.minor).(string); got != c.want {
			t.Errorf("amount(%d) = %q, want %q", c.minor, got, c.want)
		}
	}
}

func TestAgoFunc(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{"never saved", time.Time{}, "—"},
		{"seconds", now.Add(-20 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5 min ago"},
		{"hours", now.Add(-3 * time.Hour), "3 hr ago"},
		{"yesterday", now.Add(-30 * time.Hour), "yesterday"},
		{"days", now.Add(-5 * 24 * time.Hour), "5 days ago"},
	}
	for _, c := range cases {
		if got := call(t, "ago", c.when).(string); got != c.want {
			t.Errorf("%s: ago = %q, want %q", c.name, got, c.want)
		}
	}
	// Anything older than a month is given as a date rather than a count.
	old := time.Date(2025, time.March, 9, 14, 0, 0, 0, time.UTC)
	if got := call(t, "ago", old).(string); got != "9 Mar 2025" {
		t.Errorf("ago(a date last year) = %q, want %q", got, "9 Mar 2025")
	}
}

func TestDictFunc(t *testing.T) {
	fn := templateFuncs["dict"].(func(...any) (map[string]any, error))

	got, err := fn("D", "a dress", "Cur", "₦", "Ordering", true)
	if err != nil {
		t.Fatalf("dict: %v", err)
	}
	want := map[string]any{"D": "a dress", "Cur": "₦", "Ordering": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dict = %v, want %v", got, want)
	}
	if _, err := fn("D"); err == nil {
		t.Error("an odd number of arguments was accepted")
	}
	if _, err := fn(1, "one"); err == nil {
		t.Error("a key that is not a string was accepted")
	}
	if m, err := fn(); err != nil || len(m) != 0 {
		t.Errorf("dict() = %v, %v, want an empty map", m, err)
	}
}

func TestSocialFuncs(t *testing.T) {
	cases := []struct{ fn, in, want string }{
		{"instagram", "@ajumafashionhub", "https://instagram.com/ajumafashionhub"},
		{"instagram", "ajumafashionhub", "https://instagram.com/ajumafashionhub"},
		{"instagram", "https://www.instagram.com/ajumafashionhub/", "https://www.instagram.com/ajumafashionhub/"},
		{"instagram", "instagram.com/ajumafashionhub", "https://instagram.com/ajumafashionhub"},
		{"instagram", "", ""},
		{"tiktok", "@ajumafashionhub", "https://tiktok.com/@ajumafashionhub"},
		{"tiktok", " ajumafashionhub ", "https://tiktok.com/@ajumafashionhub"},
		{"tiktok", "http://tiktok.com/@ajumafashionhub", "http://tiktok.com/@ajumafashionhub"},
		{"tiktok", "", ""},
	}
	for _, c := range cases {
		if got := call(t, c.fn, c.in).(string); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.fn, c.in, got, c.want)
		}
	}
	if got := call(t, "year").(int); got != time.Now().Year() {
		t.Errorf("year() = %d, want %d", got, time.Now().Year())
	}
	if got := call(t, "phone", "2348155604988").(string); got != "+234 815 560 4988" {
		t.Errorf("phone = %q, want the number spaced out", got)
	}
}

func TestRendererPairsEveryPageWithItsLayout(t *testing.T) {
	r, err := NewRenderer(embedded, false)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	wantPages := []string{
		"home.html", "dress.html", "error.html",
		"admin_login.html", "admin_index.html", "admin_form.html", "admin_settings.html",
	}
	if len(r.pages) != len(wantPages) {
		t.Errorf("%d pages were parsed, want %d — a page was added without a test",
			len(r.pages), len(wantPages))
	}
	for _, name := range wantPages {
		tmpl, ok := r.pages[name]
		if !ok {
			t.Errorf("%s was not registered", name)
			continue
		}
		// The layout is the template that gets executed, so its name tells us
		// which chrome the page was given.
		want := "site.html"
		if strings.HasPrefix(name, "admin") {
			want = "admin.html"
		}
		if tmpl.Name() != want {
			t.Errorf("%s was paired with %s, want %s", name, tmpl.Name(), want)
		}
		// Both partials are available to every page.
		for _, partial := range []string{"card", "figure"} {
			if tmpl.Lookup(partial) == nil {
				t.Errorf("%s cannot use the %q partial", name, partial)
			}
		}
	}
}

func TestNewRendererRefusesAnEmptyOrBrokenSet(t *testing.T) {
	if _, err := NewRenderer(fstest.MapFS{}, false); err == nil {
		t.Error("a set with no pages was accepted")
	} else if !strings.Contains(err.Error(), "no templates found") {
		t.Errorf("error = %v, want it to say no templates were found", err)
	}

	broken := fstest.MapFS{
		"templates/layouts/site.html": &fstest.MapFile{Data: []byte(`{{block "main" .}}{{end}}`)},
		"templates/pages/home.html":   &fstest.MapFile{Data: []byte(`{{define "main"}}{{noSuchFunc .}}{{end}}`)},
	}
	_, err := NewRenderer(broken, false)
	if err == nil {
		t.Fatal("a page that does not parse was accepted")
	}
	if !strings.Contains(err.Error(), "parse home.html") {
		t.Errorf("error = %v, want it to name the page that failed", err)
	}
}

// twoPageFS is the smallest template set that renders: one layout, one page.
func twoPageFS(body string) fstest.MapFS {
	return fstest.MapFS{
		"templates/layouts/site.html": &fstest.MapFile{Data: []byte(`<p>{{block "main" .}}{{end}}</p>`)},
		"templates/pages/home.html":   &fstest.MapFile{Data: []byte(`{{define "main"}}` + body + `{{end}}`)},
	}
}

func TestRendererDevModeRereadsTheTemplates(t *testing.T) {
	fsys := twoPageFS("first wording")

	live, err := NewRenderer(fsys, true)
	if err != nil {
		t.Fatalf("NewRenderer(dev): %v", err)
	}
	fixed, err := NewRenderer(fsys, false)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	render := func(r *Renderer) string {
		rec := httptest.NewRecorder()
		r.Render(rec, http.StatusOK, "home.html", nil)
		return rec.Body.String()
	}
	if got := render(live); !strings.Contains(got, "first wording") {
		t.Fatalf("body = %q, want the first wording", got)
	}

	// The owner edits a page while the shop is running.
	fsys["templates/pages/home.html"] = &fstest.MapFile{Data: []byte(`{{define "main"}}second wording{{end}}`)}

	if got := render(live); !strings.Contains(got, "second wording") {
		t.Errorf("with -dev the body is %q, want the edit picked up", got)
	}
	if got := render(fixed); !strings.Contains(got, "first wording") {
		t.Errorf("without -dev the body is %q, want the parsed-at-boot version", got)
	}
}

func TestRenderHidesATornPage(t *testing.T) {
	r, err := NewRenderer(twoPageFS(`before{{.Missing}}after`), false)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	// A page that fails halfway must not reach the visitor behind a 200: the
	// template is executed into a buffer, so a failure becomes a clean 500.
	rec := httptest.NewRecorder()
	r.Render(rec, http.StatusOK, "home.html", struct{}{})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "before") {
		t.Errorf("body = %q, want none of the half-written page", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "temporarily unavailable") {
		t.Errorf("body = %q, want the polite failure", rec.Body.String())
	}

	// A page nobody registered is the same kind of failure.
	rec = httptest.NewRecorder()
	r.Render(rec, http.StatusOK, "nowhere.html", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status for an unregistered page = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if _, err := r.lookup("nowhere.html"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("lookup error = %v, want it to say the page is not registered", err)
	}
}

func TestStaticHandlerServesTheAssetsWithCaching(t *testing.T) {
	static, err := StaticHandler(embedded)
	if err != nil {
		t.Fatalf("StaticHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	static.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/base.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q, want an hour", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if !strings.Contains(rec.Body.String(), "--ink") {
		t.Error("the stylesheet does not look like the shop's stylesheet")
	}

	rec = httptest.NewRecorder()
	static.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/nothing.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status for a missing asset = %d, want 404", rec.Code)
	}
}

func TestMediaHandlerServesUploadsAndNothingElse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatalf("make the uploads directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img_1.jpg"), []byte("pretend JPEG"), 0o644); err != nil {
		t.Fatalf("write a photograph: %v", err)
	}
	// The catalogue sits beside the uploads directory, one level up.
	const secret = "the whole catalogue"
	if err := os.WriteFile(filepath.Join(root, "catalogue.json"), []byte(secret), 0o644); err != nil {
		t.Fatalf("write the catalogue: %v", err)
	}

	h := MediaHandler(dir)
	ask := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	got := ask("/media/img_1.jpg")
	if got.Code != http.StatusOK || got.Body.String() != "pretend JPEG" {
		t.Fatalf("a photograph came back as %d %q", got.Code, got.Body.String())
	}
	if want := "public, max-age=604800, immutable"; got.Header().Get("Cache-Control") != want {
		t.Errorf("Cache-Control = %q, want %q", got.Header().Get("Cache-Control"), want)
	}
	if got.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got.Header().Get("X-Content-Type-Options"))
	}

	// The uploads folder is not browsable, at any depth.
	for _, target := range []string{"/media/", "/media/inner/"} {
		if code := ask(target).Code; code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 rather than a directory listing", target, code)
		}
	}

	// And nothing outside it can be reached, however the path is spelled.
	for _, target := range []string{
		"/media/../catalogue.json",
		"/media/..%2Fcatalogue.json",
		"/media/inner/../../catalogue.json",
		"/media/%2e%2e/catalogue.json",
	} {
		res := ask(target)
		if strings.Contains(res.Body.String(), secret) {
			t.Errorf("%s served the catalogue: %d %q", target, res.Code, res.Body.String())
		}
	}
}
