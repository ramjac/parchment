package workspace

import (
	"context"
	"os"
	"path/filepath"
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
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "spreadsheet.json" {
		t.Fatalf("spreadsheet artifact files = %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "spreadsheet.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := spreadsheet.Decode(data)
	if err != nil || decoded.Title != "Simple" || decoded.Sheets[0].Rows[1][0].Value != "Book" {
		t.Fatalf("decoded workbook = %+v, %v", decoded, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "spreadsheet.json")); err != nil || info.Mode().Perm() != 0o600 {
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
