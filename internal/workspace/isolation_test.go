package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/spreadsheet"
)

func TestWriteAtomicHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeAtomic(ctx, path, []byte("new"), 0o600); err == nil {
		t.Fatal("canceled write succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Fatalf("file replaced: %q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestCloneHelpersDoNotShareState(t *testing.T) {
	original := note.Note{Blocks: map[string]json.RawMessage{"parchment-x": json.RawMessage("{}")}}
	clone := cloneNote(original)
	clone.Blocks["parchment-x"][0] = 'X'
	if string(original.Blocks["parchment-x"]) != "{}" {
		t.Fatalf("note clone shares payload: %s", original.Blocks["parchment-x"])
	}

	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "a", Rows: [][]spreadsheet.Cell{{{Value: "1"}}}}}}
	bookClone := cloneSpreadsheet(book)
	bookClone.Sheets[0].Rows[0][0].Value = "2"
	if book.Sheets[0].Rows[0][0].Value != "1" {
		t.Fatal("spreadsheet clone shares cells")
	}

	resolved := time.Now()
	change := document.Change{ResolvedAt: &resolved, After: document.ChangeSnapshot{ImageNames: []string{"a"}}}
	changeClone := cloneChange(change)
	changeClone.After.ImageNames[0] = "b"
	*changeClone.ResolvedAt = resolved.Add(time.Hour)
	if change.After.ImageNames[0] != "a" || !change.ResolvedAt.Equal(resolved) {
		t.Fatal("change clone shares state")
	}
}
