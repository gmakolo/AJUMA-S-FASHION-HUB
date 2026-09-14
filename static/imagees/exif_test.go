package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ifdEntry is one record in a TIFF directory: which tag, what kind of value,
// and the value itself.
type ifdEntry struct{ tag, typ, value uint16 }

// tiffBlock builds the TIFF header an EXIF block carries, with IFD0 holding
// the entries given.
func tiffBlock(order binary.ByteOrder, entries ...ifdEntry) []byte {
	b := make([]byte, 8+2+12*len(entries)+4)
	if order == binary.ByteOrder(binary.LittleEndian) {
		copy(b, "II")
	} else {
		copy(b, "MM")
	}
	order.PutUint16(b[2:], 42)
	order.PutUint32(b[4:], 8) // IFD0 begins straight after the header
	order.PutUint16(b[8:], uint16(len(entries)))
	for i, e := range entries {
		at := 10 + i*12
		order.PutUint16(b[at:], e.tag)
		order.PutUint16(b[at+2:], e.typ)
		order.PutUint32(b[at+4:], 1)       // one value
		order.PutUint16(b[at+8:], e.value) // a SHORT sits in the first half of the field
	}
	return b
}

const (
	tagOrientation = 0x0112
	typeShort      = 3
	typeLong       = 4
)

// exifHeader is the marker that opens an APP1 EXIF payload, spelled without
// escapes so it survives every editor.
var exifHeader = []byte{'E', 'x', 'i', 'f', 0, 0}

// segment wraps a payload in a JPEG marker segment, size included.
func segment(marker byte, payload []byte) []byte {
	out := []byte{0xFF, marker}
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	return append(out, payload...)
}

// exifSegment is a complete APP1 segment recording one orientation.
func exifSegment(order binary.ByteOrder, orientation uint16) []byte {
	return segment(0xE1, append(append([]byte{}, exifHeader...),
		tiffBlock(order, ifdEntry{tagOrientation, typeShort, orientation})...))
}

// jpegWith assembles the bytes of a JPEG-shaped file from the parts given.
func jpegWith(parts ...[]byte) []byte {
	out := []byte{0xFF, 0xD8} // SOI
	for _, p := range parts {
		out = append(out, p...)
	}
	return append(out, 0xFF, 0xD9) // EOI
}

func TestJPEGOrientation(t *testing.T) {
	le, be := binary.ByteOrder(binary.LittleEndian), binary.ByteOrder(binary.BigEndian)

	// A comment segment that spells out an EXIF block: a reader that simply
	// searched for "Exif" would take its orientation from here.
	forgedComment := segment(0xFE, append(append([]byte{}, exifHeader...),
		tiffBlock(le, ifdEntry{tagOrientation, typeShort, 6})...))

	cases := []struct {
		name string
		raw  []byte
		want int
	}{
		{"a plain JPEG is upright", jpegWith(), 1},
		{"little-endian, rotate 90 clockwise", jpegWith(exifSegment(le, 6)), 6},
		{"big-endian, rotate 90 anticlockwise", jpegWith(exifSegment(be, 8)), 8},
		{"an explicit 1", jpegWith(exifSegment(le, 1)), 1},
		{"an orientation out of range is ignored", jpegWith(exifSegment(le, 9)), 1},
		{"another tag entirely", jpegWith(segment(0xE1, append(append([]byte{}, exifHeader...),
			tiffBlock(le, ifdEntry{0x011A, typeShort, 6})...))), 1},
		{"the wrong field type", jpegWith(segment(0xE1, append(append([]byte{}, exifHeader...),
			tiffBlock(le, ifdEntry{tagOrientation, typeLong, 6})...))), 1},
		{"the orientation tag after another", jpegWith(segment(0xE1, append(append([]byte{}, exifHeader...),
			tiffBlock(le, ifdEntry{0x010F, typeShort, 2}, ifdEntry{tagOrientation, typeShort, 3})...))), 3},
		{"a segment with no payload is stepped over", jpegWith([]byte{0xFF, 0x01}, exifSegment(le, 4)), 4},
		{"a fill byte before the marker", jpegWith([]byte{0xFF}, exifSegment(le, 6)), 6},
		{"EXIF spelled inside a comment is not read", jpegWith(forgedComment), 1},
		{"nothing is read past the start of scan", jpegWith([]byte{0xFF, 0xDA}, exifSegment(le, 6)), 1},
		{"a segment size longer than the file", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF, 0x00}, 1},
		{"a segment size below its own two bytes", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01, 0x00}, 1},
		{"out of step with the segment structure", []byte{0xFF, 0xD8, 0x12, 0x34, 0x56, 0x78}, 1},
		{"an APP1 that is not EXIF", jpegWith(segment(0xE1, []byte("http://ns.adobe.com/xap/"))), 1},
		{"not a JPEG at all", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 1},
		{"two bytes of a JPEG", []byte{0xFF, 0xD8}, 1},
		{"nothing at all", nil, 1},
	}
	for _, c := range cases {
		if got := jpegOrientation(c.raw); got != c.want {
			t.Errorf("%s: jpegOrientation = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTiffOrientationRejectsWhatItCannotTrust(t *testing.T) {
	le := binary.ByteOrder(binary.LittleEndian)
	good := func() []byte {
		return tiffBlock(le, ifdEntry{tagOrientation, typeShort, 7})
	}

	if got := tiffOrientation(good()); got != 7 {
		t.Fatalf("tiffOrientation of a sound block = %d, want 7", got)
	}

	bend := func(f func(b []byte)) []byte {
		b := good()
		f(b)
		return b
	}
	cases := []struct {
		name string
		tiff []byte
	}{
		{"nothing at all", nil},
		{"shorter than a header", []byte{'I', 'I', 42, 0}},
		{"an unknown byte order", bend(func(b []byte) { copy(b, "XX") })},
		{"the magic number is not 42", bend(func(b []byte) { le.PutUint16(b[2:], 43) })},
		{"the directory sits inside the header", bend(func(b []byte) { le.PutUint32(b[4:], 4) })},
		{"the directory sits past the end", bend(func(b []byte) { le.PutUint32(b[4:], 4096) })},
		// Forty entries claimed, one written, and the tag we want is not the
		// one that is there: the walk must stop at the end of the bytes.
		{"more entries than there are bytes", func() []byte {
			b := tiffBlock(le, ifdEntry{0x010F, typeShort, 2})
			le.PutUint16(b[8:], 40)
			return b
		}()},
		{"an orientation of zero", bend(func(b []byte) { le.PutUint16(b[18:], 0) })},
		{"an orientation past eight", bend(func(b []byte) { le.PutUint16(b[18:], 9) })},
		{"no entries", tiffBlock(le)},
	}
	for _, c := range cases {
		if got := tiffOrientation(c.tiff); got != 0 {
			t.Errorf("%s: tiffOrientation = %d, want 0", c.name, got)
		}
	}
}

// imageOf paints a grid of shades, one number per pixel, so a rotation can be
// read back as a grid and compared.
func imageOf(rows [][]int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, len(rows[0]), len(rows)))
	for y, row := range rows {
		for x, shade := range row {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(shade), G: uint8(shade), B: uint8(shade), A: 0xFF})
		}
	}
	return img
}

func gridOf(img image.Image) [][]int {
	b := img.Bounds()
	rows := make([][]int, b.Dy())
	for y := range rows {
		rows[y] = make([]int, b.Dx())
		for x := range rows[y] {
			r, _, _, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			rows[y][x] = int(r >> 8)
		}
	}
	return rows
}

func TestApplyOrientation(t *testing.T) {
	// Two across, three down, every pixel telling you where it started.
	upright := [][]int{{1, 2}, {3, 4}, {5, 6}}

	cases := []struct {
		orientation int
		name        string
		want        [][]int
	}{
		{2, "mirrored left to right", [][]int{{2, 1}, {4, 3}, {6, 5}}},
		{3, "turned half way round", [][]int{{6, 5}, {4, 3}, {2, 1}}},
		{4, "flipped top to bottom", [][]int{{5, 6}, {3, 4}, {1, 2}}},
		{5, "transposed", [][]int{{1, 3, 5}, {2, 4, 6}}},
		{6, "a quarter turn clockwise", [][]int{{5, 3, 1}, {6, 4, 2}}},
		{7, "transversed", [][]int{{6, 4, 2}, {5, 3, 1}}},
		{8, "a quarter turn anticlockwise", [][]int{{2, 4, 6}, {1, 3, 5}}},
	}
	for _, c := range cases {
		got := gridOf(applyOrientation(imageOf(upright), c.orientation))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("orientation %d (%s) gave %v, want %v", c.orientation, c.name, got, c.want)
		}
	}

	// The cases that mean "already the right way up", and anything outside the
	// tag's range, hand the picture back untouched rather than copying it.
	for _, orientation := range []int{-1, 0, 1, 9, 255} {
		src := imageOf(upright)
		if got := applyOrientation(src, orientation); got != image.Image(src) {
			t.Errorf("orientation %d did not hand the image straight back", orientation)
		}
	}

	// A decode that does not begin at the origin — a cropped source — is read
	// from its own bounds, not from (0,0).
	offset := image.NewNRGBA(image.Rect(4, 9, 6, 12))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			shade := uint8(upright[y][x])
			offset.SetNRGBA(4+x, 9+y, color.NRGBA{R: shade, G: shade, B: shade, A: 0xFF})
		}
	}
	if got, want := gridOf(applyOrientation(offset, 3)), [][]int{{6, 5}, {4, 3}, {2, 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("a cropped source turned half way round gave %v, want %v", got, want)
	}
}

// TestMediaSaveStandsAPortraitPhotographUp is the whole point of exif.go: a
// phone writes the sensor's landscape frame and records the rotation beside
// it, and the browser honours that tag only while the file is untouched. Once
// we re-encode the pixels the tag is gone, so the rotation has to be baked in.
func TestMediaSaveStandsAPortraitPhotographUp(t *testing.T) {
	m := newTestMedia(t)

	// As the sensor recorded it: 400 across, 200 down, dark on the left.
	sensor := image.NewRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(sensor, sensor.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(sensor, image.Rect(0, 0, 200, 200), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, sensor, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode the sensor frame: %v", err)
	}

	// Splice in "rotate a quarter turn clockwise", as a camera would.
	body := raw.Bytes()
	tagged := append([]byte{}, body[:2]...)
	tagged = append(tagged, exifSegment(binary.LittleEndian, 6)...)
	tagged = append(tagged, body[2:]...)

	img, err := m.Save(upload(t, "IMG_0007.JPG", tagged), "portrait")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if img.W != 200 || img.H != 400 {
		t.Fatalf("stored as %dx%d, want 200x400 — the frame should have been stood up", img.W, img.H)
	}

	saved, err := jpegAt(t, m, img.Src)
	if err != nil {
		t.Fatalf("decode the saved photograph: %v", err)
	}
	// The dark side was on the left; a quarter turn clockwise puts it on top.
	top, _, _, _ := saved.At(100, 40).RGBA()
	bottom, _, _, _ := saved.At(100, 360).RGBA()
	if top>>8 > 60 {
		t.Errorf("the top of the photograph is %d, want it dark", top>>8)
	}
	if bottom>>8 < 200 {
		t.Errorf("the bottom of the photograph is %d, want it light", bottom>>8)
	}
}

// jpegAt decodes a photograph the media store has written.
func jpegAt(t *testing.T, m *MediaStore, src string) (image.Image, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m.Dir(), strings.TrimPrefix(src, mediaURLPrefix)))
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(raw))
}
