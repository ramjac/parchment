package tui

import (
	"strings"
	"testing"

	"example.com/parchment/internal/document"
)

func BenchmarkToolbarView(b *testing.B) {
	s := &documentsScreen{
		width:  80,
		height: 24,
		documentEditorState: documentEditorState{
			layout: document.DefaultLayout(),
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.toolbarView()
	}
}

func BenchmarkWrapReviewText(b *testing.B) {
	line := "This is a typical line in a document change review containing some long text that may exceed the viewport width."
	content := strings.Repeat(line+"\n", 1000) // 1000 lines, ~110KB
	width := 80

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = wrapReviewText(content, width)
	}
}
