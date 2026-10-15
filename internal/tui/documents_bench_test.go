package tui

import (
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
