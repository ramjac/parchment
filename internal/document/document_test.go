package document

import (
	"context"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
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
