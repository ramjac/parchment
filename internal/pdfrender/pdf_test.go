package pdfrender

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/document"
)

func TestRenderUsesSelectedCoreFontAndPaginates(t *testing.T) {
	tests := []struct {
		font string
		base string
	}{
		{font: "courier", base: "/Courier"},
		{font: "helvetica", base: "/Helvetica"},
		{font: "times", base: "/Times-Roman"},
	}
	for _, tt := range tests {
		t.Run(tt.font, func(t *testing.T) {
			layout := document.DefaultLayout()
			layout.Font = tt.font
			d := document.Document{
				Body:   "First page\n\n" + document.PageBreakMarkup + "\n\nSecond page",
				Layout: layout,
			}
			var out bytes.Buffer
			if err := Render(&out, d); err != nil {
				t.Fatal(err)
			}
			pdf := out.String()
			if !strings.HasPrefix(pdf, "%PDF-") {
				t.Fatalf("output does not start with the PDF signature: %q", pdf[:min(len(pdf), 16)])
			}
			if !strings.Contains(pdf, "/BaseFont "+tt.base) {
				t.Fatalf("PDF does not use base font %s", tt.base)
			}
			if !strings.Contains(pdf, "/Count 2") {
				t.Fatalf("PDF should contain two pages")
			}
			if !strings.Contains(pdf, "%%EOF") {
				t.Fatal("PDF has no EOF marker")
			}
		})
	}
}

func TestRenderEmbedsJPEGAndTransparentPNG(t *testing.T) {
	jpegData := encodeJPEG(t)
	jpegImage, err := artifact.NewImage(jpegData)
	if err != nil {
		t.Fatal(err)
	}
	pngData := encodePNG(t)
	pngImage, err := artifact.NewImage(pngData)
	if err != nil {
		t.Fatal(err)
	}
	d := document.Document{
		Body:   "![photo](" + jpegImage.Name + ")\n\n![pixel][pixel-ref]\n\n[pixel-ref]: " + pngImage.Name,
		Layout: document.DefaultLayout(),
		Images: []artifact.Image{jpegImage, pngImage},
	}
	var out bytes.Buffer
	if err := Render(&out, d); err != nil {
		t.Fatal(err)
	}
	pdf := out.String()
	if !strings.Contains(pdf, "/DCTDecode") {
		t.Fatal("JPEG was not embedded with DCTDecode")
	}
	if !strings.Contains(pdf, "/FlateDecode") || !strings.Contains(pdf, "/SMask") {
		t.Fatal("transparent PNG was not embedded with FlateDecode and an SMask")
	}
	if countImageDraws(pdf) < 2 {
		t.Fatal("inline and reference-style images were not drawn into the PDF")
	}
}

func TestRenderResolvesImageReferencesAcrossPageBreaks(t *testing.T) {
	pngImage, err := artifact.NewImage(encodePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	d := document.Document{
		Body:   "![pixel][pixel-ref]\n\n" + document.PageBreakMarkup + "\n\n[pixel-ref]: " + pngImage.Name,
		Layout: document.DefaultLayout(),
		Images: []artifact.Image{pngImage},
	}
	var out bytes.Buffer
	if err := Render(&out, d); err != nil {
		t.Fatal(err)
	}
	if countImageDraws(out.String()) == 0 {
		t.Fatal("reference-style image before a page break was not resolved using the later definition")
	}
}

func TestPrepareImageRejectsOversizedDecodedDimensionsBeforeDecode(t *testing.T) {
	data := pngConfigOnly(uint32(maxImagePixels+1), 1)
	if _, err := prepareImage(data); err == nil || !strings.Contains(err.Error(), "exceed the PDF limit") {
		t.Fatalf("prepareImage error = %v, want decoded-pixel budget error", err)
	}
	if _, err := documentImages(document.Document{
		Images: []artifact.Image{{Name: "unreferenced", Data: data}},
	}); err == nil || !strings.Contains(err.Error(), "exceed the PDF limit") {
		t.Fatalf("unreferenced image error = %v, want decoded-pixel budget error", err)
	}
}

var pdfLengthRE = regexp.MustCompile(`/Length\s+(\d+)`)

func countImageDraws(pdf string) int {
	data := []byte(pdf)
	count := 0
	for offset := 0; offset < len(data); {
		streamAt := bytes.Index(data[offset:], []byte("stream"))
		if streamAt < 0 {
			break
		}
		streamAt += offset
		headerStart := bytes.LastIndex(data[:streamAt], []byte("<<"))
		header := data[headerStart:streamAt]
		lengthMatch := pdfLengthRE.FindSubmatch(header)
		if lengthMatch == nil || !bytes.Contains(header, []byte("/FlateDecode")) {
			offset = streamAt + len("stream")
			continue
		}
		length, err := strconv.Atoi(string(lengthMatch[1]))
		if err != nil {
			return count
		}
		contentStart := streamAt + len("stream")
		if contentStart < len(data) && data[contentStart] == '\r' {
			contentStart++
		}
		if contentStart < len(data) && data[contentStart] == '\n' {
			contentStart++
		}
		if contentStart+length > len(data) {
			return count
		}
		zr, err := zlib.NewReader(bytes.NewReader(data[contentStart : contentStart+length]))
		if err == nil {
			content, readErr := io.ReadAll(zr)
			closeErr := zr.Close()
			if readErr == nil && closeErr == nil {
				count += bytes.Count(content, []byte(" Do"))
			}
		}
		offset = contentStart + length
	}
	return count
}

func TestRenderPreservesMarkdownEmphasisAndHeadings(t *testing.T) {
	layout := document.DefaultLayout()
	layout.Font = "helvetica"
	d := document.Document{
		Body:   "# Project brief\n\nThis has **bold**, *italic*, `code`, and [a link](https://example.org).",
		Layout: layout,
	}
	var out bytes.Buffer
	if err := Render(&out, d); err != nil {
		t.Fatal(err)
	}
	pdf := out.String()
	for _, font := range []string{
		"/BaseFont /Helvetica-Bold",
		"/BaseFont /Helvetica-Oblique",
		"/BaseFont /Courier",
	} {
		if !strings.Contains(pdf, font) {
			t.Errorf("PDF is missing styled font resource %s", font)
		}
	}
}

func TestPrepareGIFAsPNG(t *testing.T) {
	var out bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}})
	img.SetColorIndex(0, 0, 1)
	if err := gif.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	asset, err := prepareImage(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if asset.format != "PNG" || asset.width != 1 || asset.height != 1 {
		t.Fatalf("GIF asset = %#v, want 1x1 PNG", asset)
	}
}

func TestRenderRejectsCharactersOutsideCoreFontEncoding(t *testing.T) {
	d := document.Document{Body: "emoji: 😀", Layout: document.DefaultLayout()}
	if err := Render(&bytes.Buffer{}, d); err == nil || !strings.Contains(err.Error(), "not supported by PDF core fonts") {
		t.Fatalf("Render error = %v, want unsupported core-font character", err)
	}
}

func TestRenderUsesLandscapePageSize(t *testing.T) {
	layout := document.DefaultLayout()
	layout.Orientation = document.Landscape
	var out bytes.Buffer
	if err := Render(&out, document.Document{Body: "Landscape", Layout: layout}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/MediaBox [0 0 792.00 612.00]") {
		t.Fatal("PDF does not use a landscape Letter page")
	}
}

func TestExpandRunningLineTokens(t *testing.T) {
	d := document.Document{
		Artifact: artifact.Artifact{Title: "Quarterly Report"},
	}
	if got := expandRunningLine("{title}|{page}/{pages}", d, 2, 5); got != "Quarterly Report|2/5" {
		t.Fatalf("expanded running line = %q", got)
	}
}

func TestWriteSupportsReusableStyledPages(t *testing.T) {
	var out bytes.Buffer
	err := Write(&out, File{
		Font: "helvetica",
		Pages: []PDFPage{{
			WidthMM:  279.4,
			HeightMM: 215.9,
			Elements: []Element{{
				Text: "Presentation slide", FontStyle: "BI", FontSize: 24, X: 20, Y: 30,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/BaseFont /Helvetica-BoldOblique") {
		t.Fatal("reusable writer did not select the bold-italic AFM font")
	}
}

func TestRenderPreservesUnsupportedNumericEntityAsReadableText(t *testing.T) {
	d := document.Document{
		Body:   "A plant: &#x1F331;.",
		Layout: document.DefaultLayout(),
	}
	var out bytes.Buffer
	if err := Render(&out, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "%PDF-") {
		t.Fatal("numeric character reference did not render to PDF")
	}
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodePNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{B: 255, A: 128})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func pngConfigOnly(width, height uint32) []byte {
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	chunk := make([]byte, 25)
	binary.BigEndian.PutUint32(chunk[:4], 13)
	copy(chunk[4:8], "IHDR")
	binary.BigEndian.PutUint32(chunk[8:12], width)
	binary.BigEndian.PutUint32(chunk[12:16], height)
	chunk[16] = 8
	chunk[17] = 6
	binary.BigEndian.PutUint32(chunk[21:], crc32.ChecksumIEEE(chunk[4:21]))
	return append(data, chunk...)
}
