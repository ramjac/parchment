package document

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders so embedded images can be validated
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strings"
)

// MaxImageBytes bounds one embedded image.
const MaxImageBytes = 10 << 20

// ImageNamePattern matches the managed file names for embedded images. The
// name is derived from the image content, so identical images share a file.
const ImageNamePattern = `image-[a-f0-9]{16}\.(?:png|jpg|gif)`

var (
	imageNameRE      = regexp.MustCompile(`^` + ImageNamePattern + `$`)
	imageReferenceRE = regexp.MustCompile(`!\[[^\]]*\]\((` + ImageNamePattern + `)\)`)
)

// Image is an embedded image stored beside the document's Markdown.
type Image struct {
	Name string
	Data []byte
}

// IsImageName reports whether name is a managed embedded image file name.
func IsImageName(name string) bool { return imageNameRE.MatchString(name) }

// NewImage validates PNG, JPEG, or GIF data and derives its stable file name.
func NewImage(data []byte) (Image, error) {
	if len(data) == 0 {
		return Image{}, errors.New("image is empty")
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("image is larger than %d MiB", MaxImageBytes>>20)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("unsupported image (use PNG, JPEG, or GIF): %w", err)
	}
	extension := format
	if format == "jpeg" {
		extension = "jpg"
	}
	return Image{Name: imageName(data, extension), Data: append([]byte(nil), data...)}, nil
}

func imageName(data []byte, extension string) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("image-%x.%s", sum[:8], extension)
}

// Validate checks that the image is a supported format within the size limit
// and that its name is the one derived from its content.
func (i Image) Validate() error {
	if !IsImageName(i.Name) {
		return fmt.Errorf("invalid image name %q", i.Name)
	}
	canonical, err := NewImage(i.Data)
	if err != nil {
		return fmt.Errorf("image %s: %w", i.Name, err)
	}
	if canonical.Name != i.Name {
		return fmt.Errorf("image %s does not match its content (expected %s)", i.Name, canonical.Name)
	}
	return nil
}

// ImageMarkdown returns the Markdown that embeds a managed image.
func ImageMarkdown(alt, name string) string {
	alt = strings.NewReplacer("[", "", "]", "", "\n", " ", "\r", " ").Replace(strings.TrimSpace(alt))
	return "![" + alt + "](" + name + ")"
}

// ReferencedImages returns the managed image names referenced by a body.
func ReferencedImages(body string) map[string]bool {
	names := map[string]bool{}
	for _, match := range imageReferenceRE.FindAllStringSubmatch(body, -1) {
		names[match[1]] = true
	}
	return names
}
