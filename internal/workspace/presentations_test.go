package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"example.com/parchment/internal/presentation"
)

func TestPresentationPersistsAsOneReadableFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	service := presentation.NewService(ws, 10)
	created, err := service.Create(ctx, "Launch", "# Launch\n\n## Opening\n\nHello **team**.\n\n: say hello\n")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "parchment", "artifacts", created.ID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "content.md" {
		t.Fatalf("presentation artifact files = %v, %v", entries, err)
	}
	info, err := os.Stat(filepath.Join(dir, "content.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("presentation permissions = %v, %v", info, err)
	}
	restored, err := presentation.NewService(ws, 10).Get(ctx, created.ID)
	if err != nil || restored.Source != created.Source || restored.Title != created.Title {
		t.Fatalf("restored presentation = %+v, %v", restored, err)
	}
	items, err := presentation.NewService(ws, 10).List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("presentation list = %v, %v", items, err)
	}
}
