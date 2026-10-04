package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestStandaloneSpreadsheetEditsWithoutWorkspace(t *testing.T) {
	sample, err := os.ReadFile("../../examples/budget.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "budget.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenSpreadsheetFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := spreadsheet.NewService(repository, 10)
	items, err := service.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	book, err := service.Get(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "budget" || repository.ArtifactLocation(book.ID) != book.Location {
		t.Fatalf("unexpected workbook: %+v", book.Artifact)
	}
	if _, err := service.SetCell(context.Background(), book.ID, 2, 2, spreadsheet.Cell{Value: "200"}); err != nil {
		t.Fatal(err)
	}
	reopenedRepository, err := OpenSpreadsheetFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := reopenedRepository.GetSpreadsheet(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Sheets[0].Rows[1][1].Value != "200" ||
		reopened.Sheets[0].Rows[1][3].Formula != "=B2-C2" ||
		reopened.Body != book.Body || reopened.Location != book.Location {
		t.Fatalf("save lost workbook data: %+v", reopened)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v", info.Mode().Perm())
	}
	if _, err := service.Undo(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopenedRepository, err = OpenSpreadsheetFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err = reopenedRepository.GetSpreadsheet(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Sheets[0].Rows[1][1].Value != "180" {
		t.Fatalf("undo did not persist: %+v", reopened.Sheets[0].Rows[1][1])
	}
}

func TestStandaloneSpreadsheetRejectsExternalChanges(t *testing.T) {
	sample, err := os.ReadFile("../../examples/budget.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "budget.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenSpreadsheetFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(sample, []byte("\nExternal edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	service := spreadsheet.NewService(repository, 10)
	items, err := repository.ListSpreadsheets(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	if _, err := service.SetCell(context.Background(), items[0].ID, 2, 2, spreadsheet.Cell{Value: "200"}); err == nil ||
		!strings.Contains(err.Error(), "changed outside Parchment") {
		t.Fatalf("external edit error = %v", err)
	}
}
