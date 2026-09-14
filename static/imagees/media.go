package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registered so a GIF upload decodes
	"image/jpeg"
	_ "image/png" // registered so a PNG upload decodes
	"io"
	"math"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxUploadBytes    = 8 << 20 // 8 MB per photograph
	maxImageWidth     = 1400
	maxImageHeight    = 2000
	jpegQuality       = 84
	maxImagesPerDress = 5
	mediaURLPrefix    = "/media/"
)

// MediaStore owns the uploads directory. Uploaded bytes are never trusted:
// every file is decoded, re-encoded as JPEG and written under a name we
// generate, so nothing the browser sent survives as a file name, a container
// or embedded metadata.
type MediaStore struct{ dir string }

// OpenMediaStore prepares the uploads directory.
func OpenMediaStore(dir string) (*MediaStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create uploads directory: %w", err)
	}
	return &MediaStore{dir: dir}, nil
}

// Dir is the directory uploads are served from.
func (m *MediaStore) Dir() string { return m.dir }

// Save processes one uploaded photograph and returns the Image record to
// store against a dress.
func (m *MediaStore) Save(header *multipart.FileHeader, alt string) (Image, error) {
	if header.Size > maxUploadBytes {
		return Image{}, fmt.Errorf("%q is larger than 8 MB — please shrink it first", header.Filename)
	}
	file, err := header.Open()
	if err != nil {
		return Image{}, fmt.Errorf("open upload: %w", err)
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("read upload: %w", err)
	}
	if len(raw) > maxUploadBytes {
		return Image{}, errors.New("that photograph is larger than 8 MB")
	}

	decoded, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Image{}, fmt.Errorf("%q is not a readable JPEG, PNG or GIF", header.Filename)
	}
	if format == "jpeg" {
		decoded = applyOrientation(decoded, jpegOrientation(raw))
	}

	final := downscale(flatten(decoded), maxImageWidth, maxImageHeight)

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, final, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return Image{}, fmt.Errorf("encode photograph: %w", err)
	}

	name := NewID("img_") + ".jpg"
	target := filepath.Join(m.dir, name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, encoded.Bytes(), 0o644); err != nil {
		return Image{}, fmt.Errorf("save photograph: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return Image{}, fmt.Errorf("save photograph: %w", err)
	}

	return Image{
		Src:  mediaURLPrefix + name,
		Alt:  trimTo(oneLine(alt), 160),
		W:    final.Rect.Dx(),
		H:    final.Rect.Dy(),
		Tint: averageTint(final),
	}, nil
}

// Remove deletes an uploaded file. Anything outside the uploads directory —
// a seeded illustration under /static, or a crafted path — is ignored rather
// than followed.
func (m *MediaStore) Remove(src string) error {
	if !strings.HasPrefix(src, mediaURLPrefix) {
		return nil
	}
	name := path.Base(strings.TrimPrefix(src, mediaURLPrefix))
	if name == "." || name == ".." || name == "/" || name == "" {
		return nil
	}
	err := os.Remove(filepath.Join(m.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RemoveAll deletes every uploaded file belonging to a dress.
func (m *MediaStore) RemoveAll(images []Image) {
	for _, img := range images {
		_ = m.Remove(img.Src)
	}
}

// flatten composites the image onto white, so a transparent PNG does not
// turn into a black rectangle when it is encoded as JPEG.
func flatten(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// downscale shrinks an image to fit maxW by maxH using a box filter, and
// averages in linear light rather than straight on the sRGB values — the
// difference is visible on fabric, where naive averaging greys out pattern.
func downscale(src *image.RGBA, maxW, maxH int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == 0 || sh == 0 {
		return src
	}
	scale := math.Min(float64(maxW)/float64(sw), float64(maxH)/float64(sh))
	if scale >= 1 {
		return src
	}
	dw := max(1, int(float64(sw)*scale+0.5))
	dh := max(1, int(float64(sh)*scale+0.5))

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		y0 := dy * sh / dh
		y1 := max(y0+1, (dy+1)*sh/dh)
		for dx := 0; dx < dw; dx++ {
			x0 := dx * sw / dw
			x1 := max(x0+1, (dx+1)*sw/dw)

			var r, g, b float64
			var n float64
			for y := y0; y < y1; y++ {
				row := src.PixOffset(src.Rect.Min.X+x0, src.Rect.Min.Y+y)
				for x := x0; x < x1; x++ {
					p := row + (x-x0)*4
					r += srgbToLinear[src.Pix[p]]
					g += srgbToLinear[src.Pix[p+1]]
					b += srgbToLinear[src.Pix[p+2]]
					n++
				}
			}
			o := dst.PixOffset(dx, dy)
			dst.Pix[o] = linearToSRGB(r / n)
			dst.Pix[o+1] = linearToSRGB(g / n)
			dst.Pix[o+2] = linearToSRGB(b / n)
			dst.Pix[o+3] = 0xFF
		}
	}
	return dst
}

// averageTint is the mean colour of the photograph, used as the placeholder
// behind it so a slow connection sees the fabric's colour rather than a
// white hole in the lookbook.
func averageTint(img *image.RGBA) string {
	var r, g, b, n float64
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			p := img.PixOffset(x, y)
			r += srgbToLinear[img.Pix[p]]
			g += srgbToLinear[img.Pix[p+1]]
			b += srgbToLinear[img.Pix[p+2]]
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x",
		linearToSRGB(r/n), linearToSRGB(g/n), linearToSRGB(b/n))
}

// srgbToLinear undoes the sRGB transfer curve for all 256 byte values.
var srgbToLinear = func() (t [256]float64) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = c / 12.92
		} else {
			t[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return t
}()

// linearToSRGB re-applies the sRGB transfer curve and clamps to a byte.
func linearToSRGB(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 255
	case v <= 0.0031308:
		return uint8(v*12.92*255 + 0.5)
	default:
		return uint8((1.055*math.Pow(v, 1/2.4)-0.055)*255 + 0.5)
	}
}
