package docinterop

import (
	"archive/zip"
	"bytes"
	"example.com/parchment/internal/document"
	"strings"
	"testing"
)

func TestReadZipLimits(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	// Create a dummy file
	f, err := zw.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hello"))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())

	// Test exceeding size limit
	_, err = ReadZip(reader, MaxInputSize+1)
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
