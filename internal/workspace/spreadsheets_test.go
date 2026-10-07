package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestSpreadsheetIsSingleInspectableFileWithMetadataAndUndo(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	service := spreadsheet.NewService(ws, 10)
	created, err := service.Create(ctx, "Simple", [][]spreadsheet.Cell{
		{{Value: "Item"}, {Value: "Cost"}},
		{{Value: "Book"}, {Value: "5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "artifacts", created.ID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "content.md" {
		t.Fatalf("spreadsheet artifact files = %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := spreadsheet.Decode(data)
	if err != nil || decoded.Title != "Simple" || decoded.Sheets[0].Rows[1][0].Value != "Book" {
		t.Fatalf("decoded workbook = %+v, %v", decoded, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "content.md")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("spreadsheet file permissions = %v, %v", info, err)
	}

	updated, err := service.SetCell(ctx, created.ID, 2, 2, spreadsheet.Cell{Value: "7"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Sheets[0].Rows[1][1].Value != "7" {
		t.Fatalf("updated spreadsheet = %+v", updated)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	undone, err := spreadsheet.NewService(ws, 10).Get(ctx, created.ID)
	if err != nil || undone.Sheets[0].Rows[1][1].Value != "5" {
		t.Fatalf("spreadsheet after undo = %+v, %v", undone, err)
	}
}

func TestInvalidUTF8CellDoesNotPersistOrPolluteUndoHistory(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := spreadsheet.NewService(ws, 10)
	created, err := service.Create(ctx, "Values", [][]spreadsheet.Cell{{{Value: "before"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCell(ctx, created.ID, 1, 1, spreadsheet.Cell{Value: "after"}); err != nil {
		t.Fatal(err)
	}
	invalid := string([]byte{0xff})
	if _, err := service.SetCell(ctx, created.ID, 1, 1, spreadsheet.Cell{Value: invalid}); err == nil ||
		!strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid cell update returned %v", err)
	}
	current, err := service.Get(ctx, created.ID)
	if err != nil || current.Sheets[0].Rows[0][0].Value != "after" {
		t.Fatalf("spreadsheet after rejected update = %+v, %v", current, err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatalf("undo after rejected update: %v", err)
	}
	undone, err := service.Get(ctx, created.ID)
	if err != nil || undone.Sheets[0].Rows[0][0].Value != "before" {
		t.Fatalf("spreadsheet after undo = %+v, %v", undone, err)
	}
}
