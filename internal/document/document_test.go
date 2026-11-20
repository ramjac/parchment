package document

import (
	"context"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/spreadsheet"
)

func TestDocumentSnapshotsDoNotTrackTransientTitle(t *testing.T) {
	before := Document{Artifact: artifact.Artifact{Title: "Filename title"}, Body: "Text", Layout: DefaultLayout()}
	after := before
	after.Title = "Changed transient title"

	if !Equal(before, after) {
		t.Fatal("document equality includes transient title changes")
	}
}

func TestNormalizeAllowsBlankTitle(t *testing.T) {
	service := NewService(&documentRepositoryStub{}, 5)
	created, err := service.Create(context.Background(), "doc.md", Draft{
		Body:   "Text",
		Layout: DefaultLayout(),
	})
	if err != nil {
		t.Fatalf("create document without a title: %v", err)
	}
	if created.Title != "" {
		t.Fatalf("create populated transient title with %q", created.Title)
	}

	updated, err := service.Save(context.Background(), created, Draft{
		Body:   "Updated text",
		Layout: DefaultLayout(),
	})
	if err != nil {
		t.Fatalf("save document: %v", err)
	}
	if updated.Title != "" {
		t.Fatalf("save changed transient title to %q", updated.Title)
	}
}

func TestModifyWithTrackingChoiceUsesCurrentSnapshot(t *testing.T) {
	service := NewService(&documentRepositoryStub{}, 5)
	ctx := context.Background()
	if _, err := service.Create(ctx, "doc.md", Draft{Body: "before", Layout: DefaultLayout()}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetTracking(ctx, "doc.md", true); err != nil {
		t.Fatal(err)
	}
	edit := func(d *Document) error { d.Body = "after"; return nil }
	if _, err := service.ModifyWithTrackingChoice(ctx, "doc.md", "edit", nil, edit); err == nil || !strings.Contains(err.Error(), "choose --track") {
		t.Fatalf("missing tracking choice: %v", err)
	}
	current, err := service.Get(ctx, "doc.md")
	if err != nil || current.Body != "before" {
		t.Fatalf("rejected edit mutated document: %+v, %v", current, err)
	}
	track := false
	current, err = service.ModifyWithTrackingChoice(ctx, "doc.md", "edit", &track, edit)
	if err != nil || current.Body != "after" || len(current.TrackedChanges) != 0 {
		t.Fatalf("explicit untracked edit: %+v, %v", current, err)
	}
	track = true
	current, err = service.ModifyWithTrackingChoice(ctx, "doc.md", "edit", &track, func(d *Document) error {
		d.Body = "again"
		return nil
	})
	if err != nil || len(current.TrackedChanges) != 1 {
		t.Fatalf("explicit tracked edit: %+v, %v", current, err)
	}
}

func TestTrackedWorkbookSessionGroupsWorkbookAndReference(t *testing.T) {
	ctx := context.Background()
	service := NewService(&documentRepositoryStub{}, 5)
	created, err := service.Create(ctx, "doc.md", Draft{Body: "before", Layout: DefaultLayout()})
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := service.SetTrackingExpected(ctx, created, true)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := spreadsheet.NewEmbeddedSheet("Data")
	if err != nil {
		t.Fatal(err)
	}
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sheet}}
	if err := book.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Data", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "4"},
	}); err != nil {
		t.Fatal(err)
	}
	reference, err := spreadsheet.FormatReference(spreadsheet.Reference{
		Kind: spreadsheet.SheetReferenceKind, ID: sheet.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Save(ctx, tracked, Draft{
		Body: "before\n\n" + reference, Layout: tracked.Layout, Workbook: book,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.TrackedChanges) != 2 {
		t.Fatalf("spreadsheet session changes = %+v", updated.TrackedChanges)
	}
	var workbookChange TrackedChange
	for _, change := range updated.TrackedChanges {
		if change.Kind == trackedWorkbookKind {
			workbookChange = change
		}
	}
	if workbookChange.ID == "" || workbookChange.Group != workbookChange.ID {
		t.Fatalf("missing grouped workbook change: %+v", updated.TrackedChanges)
	}
	for _, change := range updated.TrackedChanges {
		if change.Group != workbookChange.Group {
			t.Fatalf("reference change was not grouped with workbook: %+v", updated.TrackedChanges)
		}
	}
	nextBook := updated.Workbook.Clone()
	if err := nextBook.SetCell(spreadsheet.SetCellOptions{
		Sheet: "Data", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "5"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, updated, Draft{
		Body: updated.Body, Layout: updated.Layout,
		Workbook: nextBook,
	}); err == nil || !strings.Contains(err.Error(), "review pending spreadsheet changes") {
		t.Fatalf("dependent workbook edit was not blocked: %v", err)
	}
	rejected, err := service.ReviewTracked(ctx, updated, workbookChange.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Body != "before" || len(rejected.Workbook.Sheets) != 0 || len(rejected.TrackedChanges) != 0 {
		t.Fatalf("reject did not restore the spreadsheet session: %+v", rejected)
	}
	accepted := updated
	if err := ResolveTrackedChange(&accepted, workbookChange.ID, true); err != nil {
		t.Fatal(err)
	}
	if accepted.Body != updated.Body || !accepted.Workbook.Equal(updated.Workbook) || len(accepted.TrackedChanges) != 0 {
		t.Fatalf("accept did not retain the spreadsheet session: %+v", accepted)
	}
}

type documentRepositoryStub struct {
	Repository
	stored Document
}

func (r *documentRepositoryStub) GetDocument(context.Context, string) (Document, error) {
	return r.stored, nil
}

func (r *documentRepositoryStub) TransitionDocument(
	_ context.Context, _ string, _ *Document, target *Document,
) error {
	r.stored = *target
	return nil
}
