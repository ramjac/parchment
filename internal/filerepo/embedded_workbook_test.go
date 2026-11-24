package filerepo_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/presentation"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

func TestEmbeddedWorkbookRoundTripsHostArtifacts(t *testing.T) {
	ctx := context.Background()
	repo, err := filerepo.New(filepath.Join(t.TempDir(), "state"), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sales, err := spreadsheet.NewEmbeddedSheet("Sales")
	if err != nil {
		t.Fatal(err)
	}
	charts, err := spreadsheet.NewEmbeddedSheet("Charts")
	if err != nil {
		t.Fatal(err)
	}
	workbook := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sales, charts}}
	if err := workbook.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Sales", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "3"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := workbook.AddChart(spreadsheet.Chart{
		ID: "chart-1", Type: "bar", Sheet: "Sales", FirstRow: 1, LastRow: 1,
		FirstColumn: 1, LastColumn: 1, PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1,
	}); err != nil {
		t.Fatal(err)
	}

	docPath := filepath.Join(t.TempDir(), "report.md")
	docs := document.NewService(repo, 10)
	created, err := docs.Create(ctx, docPath, document.Draft{
		Body:   "![Spreadsheet](parchment-sheet:" + sales.ID + ")",
		Layout: document.DefaultLayout(), Workbook: workbook,
	})
	if err != nil {
		t.Fatal(err)
	}
	loadedDoc, err := docs.Get(ctx, docPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created.Workbook, loadedDoc.Workbook) {
		t.Fatal("document embedded workbook changed during persistence")
	}

	presentationPath := filepath.Join(t.TempDir(), "slides.md")
	presentations := presentation.NewService(repo, 10)
	deck, err := presentations.Create(ctx, presentationPath, "# Report\n\n## Sales\n\n: ![Spreadsheet](parchment-sheet:"+sales.ID+")\n\n")
	if err != nil {
		t.Fatal(err)
	}
	deck, err = presentations.UpdateContent(ctx, deck, deck.Source, workbook)
	if err != nil {
		t.Fatal(err)
	}
	loadedDeck, err := presentations.Get(ctx, presentationPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deck.Workbook, loadedDeck.Workbook) {
		t.Fatal("presentation embedded workbook changed during persistence")
	}
	if created.Kind != artifact.DocumentKind || deck.Kind != artifact.PresentationKind {
		t.Fatal("host artifact kinds changed")
	}
}
