package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportPerformanceFixtures(t *testing.T) {
	dir := os.Getenv("PARCHMENT_BENCH_EXPORT_DIR")
	if dir == "" {
		t.Skip("set PARCHMENT_BENCH_EXPORT_DIR to an existing directory without fixture-name conflicts")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("PARCHMENT_BENCH_EXPORT_DIR must be absolute")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("fixture destination must be an existing directory: %v", err)
	}
	novel, err := os.ReadFile(filepath.Join("testdata", "war-and-peace.md"))
	if err != nil {
		t.Fatal(err)
	}
	var guides []string
	for _, path := range []string{"README.md", "docs/User_Guide.md", "docs/Technical_Guide.md"} {
		content, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		guides = append(guides, string(content))
	}
	paths := []string{"guide-note.md", "novel-document.md", "mortgage-budget.md", "us-history.md"}
	for _, name := range paths {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("fixture destination %s already exists or is inaccessible: %v", name, err)
		}
	}
	noteFile, err := os.OpenFile(filepath.Join(dir, paths[0]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := noteFile.WriteString(strings.Join(guides, "\n"))
	closeErr := noteFile.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write guide note: %v; close: %v", writeErr, closeErr)
	}
	novelFile, err := os.OpenFile(filepath.Join(dir, paths[1]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr = novelFile.Write(novel)
	closeErr = novelFile.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write novel document: %v; close: %v", writeErr, closeErr)
	}
	workbook, err := os.ReadFile(filepath.Join("testdata", "mortgage-budget.md"))
	if err != nil {
		t.Fatal(err)
	}
	workbookFile, err := os.OpenFile(filepath.Join(dir, paths[2]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr = workbookFile.Write(workbook)
	closeErr = workbookFile.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write mortgage budget: %v; close: %v", writeErr, closeErr)
	}
	historyFile, err := os.OpenFile(filepath.Join(dir, paths[3]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr = historyFile.Write(historyArtifact)
	closeErr = historyFile.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write history presentation: %v; close: %v", writeErr, closeErr)
	}
	t.Logf("Created four standalone fixtures in %s", dir)
}
