package main

import (
	"encoding/binary"
	"image"
)

// Photographs taken on a phone are almost always stored in the sensor's own
// orientation with a rotation recorded in EXIF. Browsers honour that tag for
// a file served as-is; once we decode and re-encode the pixels ourselves the
// tag is gone, so a portrait dress would arrive on its side. The bytes below
// read the tag before we touch the pixels, and rotate to match.

// jpegOrientation returns the EXIF orientation of a JPEG (1..8), or 1 when
// the file carries no EXIF block. It walks JPEG segment markers rather than
// scanning for a byte pattern, so it cannot be fooled by image data that
// happens to spell "Exif".
func jpegOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return 1
	}
	for i := 2; i+3 < len(raw); {
		if raw[i] != 0xFF {
			return 1 // out of step with the segment structure; give up quietly
		}
		marker := raw[i+1]
		i += 2
		switch {
		case marker == 0xFF: // fill byte, re-align
			i--
			continue
		case marker == 0xD9 || marker == 0xDA: // end of image, start of scan
			return 1
		case marker >= 0xD0 && marker <= 0xD8, marker == 0x01: // no payload
			continue
		}
		if i+1 >= len(raw) {
			return 1
		}
		size := int(binary.BigEndian.Uint16(raw[i : i+2]))
		if size < 2 || i+size > len(raw) {
			return 1
		}
		segment := raw[i+2 : i+size]
		if marker == 0xE1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(segment[6:]); o != 0 {
				return o
			}
			return 1
		}
		i += size
	}
	return 1
}

// tiffOrientation reads tag 0x0112 out of IFD0 of a TIFF header, returning 0
// when the structure is not what it claims to be.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	for n := 0; n < count; n++ {
		entry := ifd + 2 + n*12
		if entry+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[entry:entry+2]) != 0x0112 {
			continue
		}
		if order.Uint16(tiff[entry+2:entry+4]) != 3 { // SHORT
			return 0
		}
		if o := int(order.Uint16(tiff[entry+8 : entry+10])); o >= 1 && o <= 8 {
			return o
		}
		return 0
	}
	return 0
}

// applyOrientation rewrites the pixels so the image stands up on its own,
// covering all eight EXIF cases including the four mirrored ones.
func applyOrientation(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 { // the diagonal cases swap the axes
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2: // mirrored
				sx, sy = w-1-x, y
			case 3: // rotated 180
				sx, sy = w-1-x, h-1-y
			case 4: // flipped
				sx, sy = x, h-1-y
			case 5: // transposed
				sx, sy = y, x
			case 6: // rotated 90 clockwise
				sx, sy = y, h-1-x
			case 7: // transversed
				sx, sy = w-1-y, h-1-x
			case 8: // rotated 90 anticlockwise
				sx, sy = w-1-y, x
			}
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}
