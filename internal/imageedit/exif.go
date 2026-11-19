package imageedit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
)

func jpegOrientation(data []byte) (int, error) {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, errors.New("invalid JPEG header")
	}
	orientation := 1
	seen := false
	for pos := 2; pos < len(data); {
		if data[pos] != 0xff {
			return 0, errors.New("invalid JPEG marker")
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			break
		}
		marker := data[pos]
		pos++
		if marker == 0xda || marker == 0xd9 {
			return orientation, nil
		}
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if pos+2 > len(data) {
			return 0, errors.New("truncated JPEG segment")
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if length < 2 || length > len(data)-pos {
			return 0, errors.New("invalid JPEG segment length")
		}
		payload := data[pos+2 : pos+length]
		if marker == 0xe1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			value, present, err := exifOrientation(payload[6:])
			if err != nil {
				return 0, err
			}
			if present {
				if seen && orientation != value {
					return 0, errors.New("conflicting EXIF orientations")
				}
				orientation, seen = value, true
			}
		}
		pos += length
	}
	return 0, errors.New("truncated JPEG header")
}

func exifOrientation(data []byte) (int, bool, error) {
	invalid := errors.New("malformed EXIF orientation metadata")
	if len(data) < 8 {
		return 0, false, invalid
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false, invalid
	}
	if order.Uint16(data[2:4]) != 42 {
		return 0, false, invalid
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset < 8 || offset+2 > uint64(len(data)) {
		return 0, false, invalid
	}
	count := uint64(order.Uint16(data[offset : offset+2]))
	start := offset + 2
	if start+count*12+4 > uint64(len(data)) {
		return 0, false, invalid
	}
	value, present := 1, false
	for n := uint64(0); n < count; n++ {
		entry := data[start+n*12 : start+(n+1)*12]
		if order.Uint16(entry[:2]) != 0x0112 {
			continue
		}
		if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			return 0, false, invalid
		}
		v := int(order.Uint16(entry[8:10]))
		if v < 1 || v > 8 || (present && v != value) {
			return 0, false, invalid
		}
		value, present = v, true
	}
	return value, present, nil
}

func orient(src image.Image, orientation int) image.Image {
	if orientation == 1 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	width, height := w, h
	if orientation >= 5 {
		width, height = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(src.Bounds().Min.X+x, src.Bounds().Min.Y+y))
		}
	}
	return dst
}
