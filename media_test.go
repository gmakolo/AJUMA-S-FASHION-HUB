package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes draws a solid rectangle and encodes it, standing in for a
// photograph coming off a phone.
func pngBytes(t *testing.T, w, h int, fill color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

// upload turns bytes into the multipart file header a real submission hands to
// MediaStore.Save, so the test enters through the same door the browser uses.
func upload(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("photos", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	parsed, err := multipart.NewReader(&body, form.Boundary()).ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	t.Cleanup(func() { _ = parsed.RemoveAll() })
	headers := parsed.File["photos"]
	if len(headers) != 1 {
		t.Fatalf("the form carries %d files, want 1", len(headers))
	}
	return headers[0]
}

func newTestMedia(t *testing.T) *MediaStore {
	t.Helper()
	m, err := OpenMediaStore(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	return m
}

func TestMediaSaveRewritesTheUpload(t *testing.T) {
	m := newTestMedia(t)
	plum := color.RGBA{R: 0x5d, G: 0x3a, B: 0x4e, A: 0xff}

	img, err := m.Save(upload(t, "IMG_4021.PNG", pngBytes(t, 900, 1200, plum)),
		"  Ebun off-shoulder dress,\n plum crepe  ")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The browser's file name never becomes a path on disk.
	if !strings.HasPrefix(img.Src, "/media/img_") || !strings.HasSuffix(img.Src, ".jpg") {
		t.Errorf("Src = %q, want a generated /media/img_….jpg name", img.Src)
	}
	if strings.Contains(strings.ToLower(img.Src), "img_4021") {
		t.Errorf("Src = %q, which carries the uploaded file's own name", img.Src)
	}
	if img.W != 900 || img.H != 1200 {
		t.Errorf("stored as %dx%d, want 900x1200 — no rescaling was needed", img.W, img.H)
	}
	if img.Alt != "Ebun off-shoulder dress, plum crepe" {
		t.Errorf("Alt = %q, want it tidied to one line", img.Alt)
	}
	if img.Tint != "#5d3a4e" {
		t.Errorf("Tint = %q, want the photograph's own colour #5d3a4e", img.Tint)
	}

	// What landed on disk is a JPEG of the size the record claims, whatever
	// was sent in.
	path := filepath.Join(m.Dir(), strings.TrimPrefix(img.Src, "/media/"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the saved photograph: %v", err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the saved photograph: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("saved as %s, want everything re-encoded as jpeg", format)
	}
	if b := decoded.Bounds(); b.Dx() != img.W || b.Dy() != img.H {
		t.Errorf("the file is %dx%d but the record says %dx%d", b.Dx(), b.Dy(), img.W, img.H)
	}
	if left, err := filepath.Glob(filepath.Join(m.Dir(), "*.tmp")); err != nil || len(left) != 0 {
		t.Errorf("a temporary file was left behind: %v", left)
	}
}

func TestMediaSaveShrinksALargePhotograph(t *testing.T) {
	m := newTestMedia(t)

	img, err := m.Save(upload(t, "big.png", pngBytes(t, 2400, 3000, color.White)), "big")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if img.W > maxImageWidth || img.H > maxImageHeight {
		t.Errorf("stored as %dx%d, want it inside %dx%d", img.W, img.H, maxImageWidth, maxImageHeight)
	}
	// 2400x3000 is 0.8 wide for its height; the shape has to survive.
	if ratio := float64(img.W) / float64(img.H); ratio < 0.79 || ratio > 0.81 {
		t.Errorf("aspect ratio came out %.3f, want about 0.800", ratio)
	}
}

func TestMediaSaveFlattensTransparency(t *testing.T) {
	m := newTestMedia(t)

	// A fully transparent PNG must not turn into a black rectangle.
	img, err := m.Save(upload(t, "clear.png", pngBytes(t, 60, 80, color.RGBA{})), "clear")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if img.Tint != "#ffffff" {
		t.Errorf("Tint = %q, want #ffffff — transparency composites onto white", img.Tint)
	}
}

func TestMediaSaveRefusesWhatItCannotRead(t *testing.T) {
	m := newTestMedia(t)

	for _, name := range []string{"notes.txt", "photograph.jpg", "shell.php"} {
		if _, err := m.Save(upload(t, name, []byte("this is not an image at all")), "alt"); err == nil {
			t.Errorf("%q was accepted", name)
		} else if !strings.Contains(err.Error(), "readable JPEG, PNG or GIF") {
			t.Errorf("%q was refused with %q, want the readable-image message", name, err)
		}
	}
	// Nothing rejected leaves a file behind.
	entries, err := os.ReadDir(m.Dir())
	if err != nil {
		t.Fatalf("read the uploads directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the uploads directory holds %d files after three rejections", len(entries))
	}
}

func TestMediaSaveRefusesAnOversizedUpload(t *testing.T) {
	m := newTestMedia(t)

	header := upload(t, "huge.png", pngBytes(t, 40, 40, color.Black))
	header.Size = maxUploadBytes + 1 // what a real 9 MB photograph reports

	if _, err := m.Save(header, "huge"); err == nil {
		t.Fatal("an upload over 8 MB was accepted")
	} else if !strings.Contains(err.Error(), "8 MB") {
		t.Errorf("refused with %q, want the 8 MB message", err)
	}
}

func TestMediaRemoveStaysInsideItsOwnDirectory(t *testing.T) {
	m := newTestMedia(t)
	parent := filepath.Dir(m.Dir())

	// A file next to the uploads directory, the sort of thing a crafted path
	// would be reaching for.
	catalogue := filepath.Join(parent, "catalogue.json")
	if err := os.WriteFile(catalogue, []byte(`{"dresses":[]}`), 0o644); err != nil {
		t.Fatalf("write the neighbouring file: %v", err)
	}

	img, err := m.Save(upload(t, "keep.png", pngBytes(t, 30, 30, color.Black)), "keep")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved := filepath.Join(m.Dir(), strings.TrimPrefix(img.Src, mediaURLPrefix))

	ignored := []string{
		"/static/img/lookbook/01.jpg",   // a seeded photograph, not ours to delete
		"/media/../catalogue.json",      // climbing out of the uploads directory
		"/media/..%2Fcatalogue.json",    // the same, encoded
		"/media/subdir/../../notes.txt", // and the long way round
		"/media/",
		"/media/..",
		"",
		"catalogue.json",
	}
	for _, src := range ignored {
		if err := m.Remove(src); err != nil {
			t.Errorf("Remove(%q) = %v, want it ignored quietly", src, err)
		}
	}
	if _, err := os.Stat(catalogue); err != nil {
		t.Errorf("the file beside the uploads directory did not survive: %v", err)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Errorf("the photograph was deleted by a path that should have been ignored: %v", err)
	}

	// Its own file, on the other hand, goes.
	if err := m.Remove(img.Src); err != nil {
		t.Fatalf("Remove(%q) = %v", img.Src, err)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Errorf("the photograph is still on disk after Remove")
	}
	// Removing it twice is not an error: the record and the file can drift.
	if err := m.Remove(img.Src); err != nil {
		t.Errorf("removing a photograph that is already gone returned %v", err)
	}
}

func TestMediaRemoveAllClearsADress(t *testing.T) {
	m := newTestMedia(t)

	var images []Image
	for i := 0; i < 3; i++ {
		img, err := m.Save(upload(t, "shot.png", pngBytes(t, 20, 20, color.Black)), "shot")
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		images = append(images, img)
	}
	images = append(images, Image{Src: "/static/img/lookbook/01.jpg"}) // seeded, must be left alone

	m.RemoveAll(images)

	entries, err := os.ReadDir(m.Dir())
	if err != nil {
		t.Fatalf("read the uploads directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d uploaded files survived RemoveAll", len(entries))
	}
}

func TestFlattenCompositesOntoWhite(t *testing.T) {
	// A source that does not begin at the origin, as a cropped decode can be.
	src := image.NewNRGBA(image.Rect(5, 7, 7, 8))
	src.SetNRGBA(5, 7, color.NRGBA{}) // fully transparent
	src.SetNRGBA(6, 7, color.NRGBA{R: 0xFF, A: 0x80})

	got := flatten(src)

	if b := got.Bounds(); b != image.Rect(0, 0, 2, 1) {
		t.Fatalf("bounds = %v, want the result moved to the origin as 2x1", b)
	}
	if r, g, b, a := got.At(0, 0).RGBA(); r != 0xFFFF || g != 0xFFFF || b != 0xFFFF || a != 0xFFFF {
		t.Errorf("transparent pixel = %v, want opaque white", got.At(0, 0))
	}
	r, g, b, a := got.RGBAAt(1, 0).R, got.RGBAAt(1, 0).G, got.RGBAAt(1, 0).B, got.RGBAAt(1, 0).A
	if a != 0xFF || r != 0xFF || g < 0x7E || g > 0x80 || g != b {
		t.Errorf("half-transparent red = (%d,%d,%d,%d), want about (255,127,127,255)", r, g, b, a)
	}
}

func TestDownscaleFitsTheBoxAndAveragesInLinearLight(t *testing.T) {
	// Left half black, right half white.
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			shade := color.RGBA{A: 0xFF}
			if x >= 2 {
				shade = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
			}
			src.SetRGBA(x, y, shade)
		}
	}

	half := downscale(src, 2, 2)
	if b := half.Bounds(); b != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v, want 2x2", b)
	}
	if got := half.RGBAAt(0, 0); got.R != 0 || got.A != 0xFF {
		t.Errorf("top-left = %v, want black — each destination pixel is one source block", got)
	}
	if got := half.RGBAAt(1, 0); got.R != 0xFF {
		t.Errorf("top-right = %v, want white", got)
	}

	// Averaged across the black/white boundary the mean is mid grey in light,
	// which is 0xBC on screen. Averaging the sRGB numbers instead would give
	// 0x80 and a picture that looks washed out.
	one := downscale(src, 1, 1)
	if b := one.Bounds(); b != image.Rect(0, 0, 1, 1) {
		t.Fatalf("bounds = %v, want 1x1", b)
	}
	if got := one.RGBAAt(0, 0); got.R < 0xBB || got.R > 0xBD {
		t.Errorf("mean of black and white = %d, want about 0xBC (naive averaging gives 0x80)", got.R)
	}

	// An image already inside the box is handed back untouched.
	if same := downscale(src, 40, 40); same != src {
		t.Error("downscale copied an image that already fits")
	}
	if empty := image.NewRGBA(image.Rect(0, 0, 0, 0)); downscale(empty, 10, 10) != empty {
		t.Error("downscale did not hand back an empty image unchanged")
	}
}

func TestAverageTint(t *testing.T) {
	solid := func(w, h int, c color.RGBA) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
		return img
	}

	cases := []struct {
		name string
		img  *image.RGBA
		want string
	}{
		{"a solid colour is its own tint", solid(8, 8, color.RGBA{R: 0x5D, G: 0x3A, B: 0x4E, A: 0xFF}), "#5d3a4e"},
		{"white", solid(3, 3, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}), "#ffffff"},
		{"black", solid(3, 3, color.RGBA{A: 0xFF}), "#000000"},
		{"an empty image has no tint", image.NewRGBA(image.Rect(0, 0, 0, 0)), ""},
	}
	for _, c := range cases {
		if got := averageTint(c.img); got != c.want {
			t.Errorf("%s: averageTint = %q, want %q", c.name, got, c.want)
		}
	}

	// Half black, half white: the mean in linear light, not 0x808080.
	split := image.NewRGBA(image.Rect(0, 0, 2, 1))
	split.SetRGBA(0, 0, color.RGBA{A: 0xFF})
	split.SetRGBA(1, 0, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
	if got := averageTint(split); got != "#bcbcbc" {
		t.Errorf("averageTint of black and white = %q, want #bcbcbc", got)
	}
}
