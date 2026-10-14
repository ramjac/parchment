package document

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sampleJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sampleGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIsImageName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid png",
			input: "image-0123456789abcdef.png",
			want:  true,
		},
		{
			name:  "valid jpg",
			input: "image-0123456789abcdef.jpg",
			want:  true,
		},
		{
			name:  "valid gif",
			input: "image-0123456789abcdef.gif",
			want:  true,
		},
		{
			name:  "valid all zeroes hash",
			input: "image-0000000000000000.png",
			want:  true,
		},
		{
			name:  "valid all letters hash",
			input: "image-aaaaaaaaaaaaaaaa.jpg",
			want:  true,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "path traversal prefix",
			input: "../image-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "path traversal suffix",
			input: "image-0123456789abcdef.png/../foo",
			want:  false,
		},
		{
			name:  "relative path directory",
			input: "images/image-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "unsupported extension jpeg",
			input: "image-0123456789abcdef.jpeg",
			want:  false,
		},
		{
			name:  "unsupported extension svg",
			input: "image-0123456789abcdef.svg",
			want:  false,
		},
		{
			name:  "unsupported extension txt",
			input: "image-0123456789abcdef.txt",
			want:  false,
		},
		{
			name:  "missing extension",
			input: "image-0123456789abcdef",
			want:  false,
		},
		{
			name:  "hash too short (15 hex chars)",
			input: "image-0123456789abcde.png",
			want:  false,
		},
		{
			name:  "hash too long (17 hex chars)",
			input: "image-0123456789abcdef0.png",
			want:  false,
		},
		{
			name:  "uppercase hex chars",
			input: "image-0123456789ABCDEF.png",
			want:  false,
		},
		{
			name:  "invalid non-hex char in hash",
			input: "image-0123456789abcdeg.png",
			want:  false,
		},
		{
			name:  "wrong prefix",
			input: "photo-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "missing prefix dash",
			input: "image0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "extra prefix character",
			input: "ximage-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "extra suffix character",
			input: "image-0123456789abcdef.pngx",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsImageName(tt.input)
			if got != tt.want {
				t.Errorf("IsImageName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestImageValidate(t *testing.T) {
	pngData := samplePNG(t)
	jpgData := sampleJPEG(t)
	gifData := sampleGIF(t)

	pngCanonical, err := NewImage(pngData)
	if err != nil {
		t.Fatalf("NewImage(pngData) failed: %v", err)
	}
	jpgCanonical, err := NewImage(jpgData)
	if err != nil {
		t.Fatalf("NewImage(jpgData) failed: %v", err)
	}
	gifCanonical, err := NewImage(gifData)
	if err != nil {
		t.Fatalf("NewImage(gifData) failed: %v", err)
	}

	tests := []struct {
		name        string
		img         Image
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid png",
			img:     pngCanonical,
			wantErr: false,
		},
		{
			name:    "valid jpeg",
			img:     jpgCanonical,
			wantErr: false,
		},
		{
			name:    "valid gif",
			img:     gifCanonical,
			wantErr: false,
		},
		{
			name:        "invalid image name format",
			img:         Image{Name: "badname.png", Data: pngData},
			wantErr:     true,
			errContains: "invalid image name \"badname.png\"",
		},
		{
			name:        "empty image name",
			img:         Image{Name: "", Data: pngData},
			wantErr:     true,
			errContains: "invalid image name \"\"",
		},
		{
			name:        "path traversal image name",
			img:         Image{Name: "../image-0123456789abcdef.png", Data: pngData},
			wantErr:     true,
			errContains: "invalid image name",
		},
		{
			name:        "empty image data",
			img:         Image{Name: pngCanonical.Name, Data: nil},
			wantErr:     true,
			errContains: "image is empty",
		},
		{
			name:        "oversized image data",
			img:         Image{Name: pngCanonical.Name, Data: make([]byte, MaxImageBytes+1)},
			wantErr:     true,
			errContains: "larger than",
		},
		{
			name:        "corrupt byte data",
			img:         Image{Name: pngCanonical.Name, Data: []byte("not an image")},
			wantErr:     true,
			errContains: "unsupported image",
		},
		{
			name:        "mismatched image hash",
			img:         Image{Name: "image-0000000000000000.png", Data: pngData},
			wantErr:     true,
			errContains: "does not match its content",
		},
		{
			name:        "mismatched image format extension",
			img:         Image{Name: strings.TrimSuffix(jpgCanonical.Name, ".jpg") + ".png", Data: jpgData},
			wantErr:     true,
			errContains: "does not match its content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.img.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Image.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Image.Validate() error = %v, want err containing %q", err, tt.errContains)
				}
			}
		})
	}
}
