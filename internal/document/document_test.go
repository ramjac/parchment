package document

import (
	"context"
	"testing"

	"example.com/parchment/internal/artifact"
)

func TestDocumentSnapshotsDoNotTrackTransientTitle(t *testing.T) {
	before := Document{Artifact: artifact.Artifact{Title: "Filename title"}, Body: "Text", Layout: DefaultLayout()}
	after := before
	after.Title = "Changed transient title"

	if !ChangeSnapshotEqual(ChangeSnapshotOf(before), ChangeSnapshotOf(after)) {
		t.Fatal("document snapshot includes transient title changes")
	}
	if !Equal(before, after) {
		t.Fatal("document equality includes transient title changes")
	}
}

func TestChangeSnapshotDoesNotRequireTitle(t *testing.T) {
	snapshot := ChangeSnapshot{Body: "Text", Layout: DefaultLayout()}
	if err := validateSnapshot(snapshot); err != nil {
		t.Fatalf("snapshot without a title was rejected: %v", err)
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
