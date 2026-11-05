package docinterop

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"example.com/parchment/internal/document"
)

func TestReadZipLimits(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, _ := zw.Create("test.txt")
	f.Write([]byte("hello"))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())

	// Test exceeding size limit
	_, err := ReadZip(reader, MaxInputSize+1)
	if err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Errorf("Expected 64 MiB limit error, got %v", err)
	}

	// Test success
	zr, err := ReadZip(reader, int64(buf.Len()))
	if err != nil {
		t.Errorf("Expected success, got %v", err)
	}
	if len(zr.File) != 1 {
		t.Errorf("Expected 1 file, got %d", len(zr.File))
	}
}

func TestReadZipUncompressedLimit(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	f, _ := zw.Create("bomb.txt")
	chunk := make([]byte, 1024*1024)
	for i := 0; i < 130; i++ {
		f.Write(chunk)
	}
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	_, err := ReadZip(reader, int64(buf.Len()))
	if err == nil || !strings.Contains(err.Error(), "128 MiB") {
		t.Errorf("Expected 128 MiB uncompressed limit error, got %v", err)
	}
}

func TestReadZipEntriesLimit(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	for i := 0; i < 1001; i++ {
		f, _ := zw.Create("test.txt")
		f.Write([]byte("h"))
	}
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	_, err := ReadZip(reader, int64(buf.Len()))
	if err == nil || !strings.Contains(err.Error(), "1000 entries") {
		t.Errorf("Expected 1000 entries limit error, got %v", err)
	}
}


func TestMapMargins(t *testing.T) {
	m, w := MapMargins(10.5, -5.0, 150.0, 25.1)
	if m.Top != 11 || m.Right != 0 || m.Bottom != document.MaxMarginMM || m.Left != 25 {
		t.Errorf("Unexpected margins: %+v", m)
	}
	if w == nil {
		t.Errorf("Expected warning for clamping/rounding")
	}

	m2, w2 := MapMargins(10.0, 10.0, 10.0, 10.0)
	if w2 != nil {
		t.Errorf("Expected no warning, got %v", w2)
	}
	if m2.Top != 10 {
		t.Errorf("Unexpected margin")
	}
}

func TestMapPageSize(t *testing.T) {
	s, o, w := MapPageSize(215.9, 279.4)
	if w != nil {
		t.Errorf("Expected exact match with no warning, got warning")
	}
	if s != document.PageLetter || o != document.Portrait {
		t.Errorf("Expected Letter Portrait, got %s %s", s, o)
	}

	s, o, w = MapPageSize(300.0, 210.0)
	if w == nil {
		t.Errorf("Expected warning on snapped layout")
	}
	if s != document.PageA4 || o != document.Landscape {
		t.Errorf("Expected A4 Landscape, got %s %s", s, o)
	}
}

func TestMapFontFamily(t *testing.T) {
	if f := MapFontFamily("Courier New"); f != "courier" { t.Errorf("Expected courier, got %s", f) }
	if f := MapFontFamily("Times New Roman"); f != "times" { t.Errorf("Expected times, got %s", f) }
	if f := MapFontFamily("Arial"); f != "helvetica" { t.Errorf("Expected helvetica, got %s", f) }
}

func TestMarkdownBuilder(t *testing.T) {
	b := &MarkdownBuilder{}
	b.WriteHeading(1, "Title", "title-1")
	b.WriteParagraph("Hello")

	expected := "# Title {#title-1}\n\nHello"
	if b.String() != expected {
		t.Errorf("Expected %q, got %q", expected, b.String())
	}
}

func TestWarningError(t *testing.T) {
	w := &Warning{Message: "test"}
	if w.Error() != "warning: test" {
		t.Errorf("Expected 'warning: test', got %q", w.Error())
	}
}

func TestXMLDecoderEntities(t *testing.T) {
	xmlData := `<!DOCTYPE root [
		<!ENTITY foo "expanded">
	]>
	<root>&foo;</root>`

	dec := NewXMLDecoder(strings.NewReader(xmlData))
	var root struct {
		Data string `xml:",chardata"`
	}
	err := dec.Decode(&root)
	if err == nil {
		t.Errorf("Expected entity error, but got nil and output %q", root.Data)
	} else if !strings.Contains(err.Error(), "invalid character entity") {
		t.Errorf("Expected invalid character entity error, got %v", err)
	}
}
