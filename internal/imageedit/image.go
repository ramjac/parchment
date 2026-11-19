// Package imageedit edits standalone PNG and JPEG files without artifact envelopes.
package imageedit

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

const (
	MaxInputBytes  = 128 << 20
	MaxDimension   = 16384
	MaxPixels      = 100_000_000
	DefaultQuality = 90
)

// Image holds normalized pixels. Editing operations return new images rather
// than modifying the original, so callers can retain previous versions.
type Image struct {
	pixels image.Image
	format string
	source any
	path   string
	bytes  int64
}

func (i *Image) Bounds() image.Rectangle { return i.pixels.Bounds() }
func (i *Image) Format() string          { return i.format }
func (i *Image) At(x, y int) color.Color { return i.pixels.At(x, y) }
func (i *Image) ColorModel() color.Model { return i.pixels.ColorModel() }
func (i *Image) Path() string            { return i.path }
func (i *Image) FileSize() int64         { return i.bytes }

// SniffFormat recognizes standalone image signatures, not filename extensions.
func SniffFormat(header []byte) string {
	switch {
	case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(header) >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff:
		return "jpeg"
	default:
		return ""
	}
}

func dimensions(width, height int) error {
	if width <= 0 || height <= 0 || width > MaxDimension || height > MaxDimension ||
		int64(width)*int64(height) > MaxPixels {
		return fmt.Errorf("image dimensions must be positive, at most %d pixels per axis and %d pixels total", MaxDimension, MaxPixels)
	}
	return nil
}

// Decode identifies the input by its contents and normalizes JPEG orientation.
func Decode(data []byte) (*Image, error) {
	if len(data) > MaxInputBytes {
		return nil, errors.New("image input exceeds 128 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image header: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return nil, errors.New("unsupported image format: use PNG or JPEG")
	}
	if err := dimensions(config.Width, config.Height); err != nil {
		return nil, err
	}
	orientation := 1
	if format == "jpeg" {
		orientation, err = jpegOrientation(data)
		if err != nil {
			return nil, err
		}
	}
	pixels, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	if decodedFormat != format || pixels.Bounds().Dx() != config.Width || pixels.Bounds().Dy() != config.Height {
		return nil, errors.New("image dimensions or format changed during decode")
	}
	return &Image{pixels: orient(pixels, orientation), format: format}, nil
}

func (i *Image) Crop(x, y, width, height int) (*Image, error) {
	bounds := i.Bounds()
	if err := dimensions(width, height); err != nil {
		return nil, err
	}
	if x < 0 || y < 0 || x > bounds.Dx()-width || y > bounds.Dy()-height {
		return nil, errors.New("crop rectangle is outside the image")
	}
	if x == 0 && y == 0 && width == bounds.Dx() && height == bounds.Dy() {
		return i, nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), i.pixels, bounds.Min.Add(image.Pt(x, y)), draw.Src)
	result := *i
	result.pixels = dst
	return &result, nil
}

// Resize uses zero to indicate an omitted dimension; callers parsing explicit
// dimensions must reject a supplied zero before calling it.
func (i *Image) Resize(width, height int) (*Image, error) {
	if width < 0 || height < 0 || (width == 0 && height == 0) ||
		width > MaxDimension || height > MaxDimension {
		return nil, errors.New("resize requires positive dimensions within the image limits")
	}
	old := i.Bounds().Size()
	if width == 0 {
		width = max(1, int((int64(height)*int64(old.X)+int64(old.Y)/2)/int64(old.Y)))
	}
	if height == 0 {
		height = max(1, int((int64(width)*int64(old.Y)+int64(old.X)/2)/int64(old.X)))
	}
	if err := dimensions(width, height); err != nil {
		return nil, err
	}
	if width == old.X && height == old.Y {
		return i, nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), i.pixels, i.Bounds(), draw.Src, nil)
	result := *i
	result.pixels = dst
	return &result, nil
}

func outputFormat(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "png", nil
	case ".jpg", ".jpeg":
		return "jpeg", nil
	default:
		return "", errors.New("image output must have a .png, .jpg, or .jpeg extension")
	}
}

func (i *Image) Encode(format string, quality int) ([]byte, error) {
	if quality < 1 || quality > 100 {
		return nil, errors.New("JPEG quality must be between 1 and 100")
	}
	var buf bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&buf, i.pixels); err != nil {
			return nil, err
		}
	case "jpeg":
		dst := image.NewRGBA(i.Bounds())
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(dst, dst.Bounds(), i.pixels, i.Bounds().Min, draw.Over)
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported output format: use PNG or JPEG")
	}
	return buf.Bytes(), nil
}
