package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/document"
)

func BenchmarkSmallDocumentReader(b *testing.B) {
	body := "# Overview\n\nA [reference][details] and an [internal link](#details).\n\n" +
		strings.Repeat("A short paragraph with **emphasis** and `code`.\n\n", 12) +
		"## Details\n\n| A | B |\n| --- | --- |\n| one | two |\n\n[details]: https://example.com\n"
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	for _, size := range []struct{ width, height int }{{60, 24}, {80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
				screen.resize(size.width, size.height)
				screen.startReading(doc)
				_ = screen.view("")
				if screen.errMessage != "" || !screen.reading || screen.snapshot.Body != body {
					b.Fatalf("reader failed or changed the source: %s", screen.errMessage)
				}
			}
		})
	}
}
