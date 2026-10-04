package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"example.com/parchment/internal/artifact"
)

func TestStandaloneExamplesOpenDirectly(t *testing.T) {
	ctx := context.Background()
	example := func(name string) string {
		t.Helper()
		path, err := filepath.Abs(filepath.Join("../../examples", name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}

	notePath := example("note.md")
	noteFile, err := OpenNoteFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := noteFile.List(ctx)
	if err != nil || len(notes) != 1 {
		t.Fatalf("open note example: %v, %v", notes, err)
	}
	if notes[0].ID != standaloneID(artifact.NoteKind, notePath) ||
		notes[0].Title != "note" || notes[0].Location != filepath.ToSlash(notePath) {
		t.Fatalf("note runtime identity = %+v", notes[0].Artifact)
	}
	noteByID, err := noteFile.Get(ctx, notes[0].ID)
	if err != nil || noteByID.ID != notes[0].ID {
		t.Fatalf("get note using listed ID = %+v, %v", noteByID.Artifact, err)
	}

	documentPath := example("document.md")
	documentFile, err := OpenDocumentFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := documentFile.ListDocuments(ctx)
	if err != nil || len(documents) != 1 {
		t.Fatalf("open document example: %v, %v", documents, err)
	}
	if documents[0].ID != standaloneID(artifact.DocumentKind, documentPath) ||
		documents[0].Title != "document" || documents[0].Location != filepath.ToSlash(documentPath) {
		t.Fatalf("document runtime identity = %+v", documents[0].Artifact)
	}
	documentByID, err := documentFile.GetDocument(ctx, documents[0].ID)
	if err != nil || documentByID.ID != documents[0].ID {
		t.Fatalf("get document using listed ID = %+v, %v", documentByID.Artifact, err)
	}

	spreadsheetPath := example("budget.md")
	spreadsheetFile, err := OpenSpreadsheetFile(spreadsheetPath)
	if err != nil {
		t.Fatal(err)
	}
	spreadsheets, err := spreadsheetFile.ListSpreadsheets(ctx)
	if err != nil || len(spreadsheets) != 1 {
		t.Fatalf("open spreadsheet example: %v, %v", spreadsheets, err)
	}
	if spreadsheets[0].ID != standaloneID(artifact.SpreadsheetKind, spreadsheetPath) ||
		spreadsheets[0].Title != "budget" || spreadsheets[0].Location != filepath.ToSlash(spreadsheetPath) {
		t.Fatalf("spreadsheet runtime identity = %+v", spreadsheets[0].Artifact)
	}
	spreadsheetByID, err := spreadsheetFile.GetSpreadsheet(ctx, spreadsheets[0].ID)
	if err != nil || spreadsheetByID.ID != spreadsheets[0].ID {
		t.Fatalf("get spreadsheet using listed ID = %+v, %v", spreadsheetByID.Artifact, err)
	}

	presentationPath := example("presentation.md")
	presentationFile, err := OpenPresentationFile(presentationPath)
	if err != nil {
		t.Fatal(err)
	}
	presentations, err := presentationFile.ListPresentations(ctx)
	if err != nil || len(presentations) != 1 {
		t.Fatalf("open presentation example: %v, %v", presentations, err)
	}
	if presentations[0].ID != standaloneID(artifact.PresentationKind, presentationPath) ||
		presentations[0].Title != "presentation" ||
		presentations[0].Location != filepath.ToSlash(presentationPath) {
		t.Fatalf("presentation runtime identity = %+v", presentations[0].Artifact)
	}
	presentationByID, err := presentationFile.GetPresentation(ctx, presentations[0].ID)
	if err != nil || presentationByID.ID != presentations[0].ID {
		t.Fatalf("get presentation using listed ID = %+v, %v", presentationByID.Artifact, err)
	}
}
