package filerepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkLoadNovelDocument(b *testing.B) {
	path := filepath.Join("..", "tui", "testdata", "war-and-peace.md")
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	repo, err := New(b.TempDir(), Options{})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.SetBytes(info.Size())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.GetDocument(ctx, path); err != nil {
			b.Fatal(err)
		}
	}
}
