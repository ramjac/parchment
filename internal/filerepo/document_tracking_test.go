package filerepo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramjac/parchment/internal/document"
)

func TestDocumentTrackingPersistsAndSupportsUndo(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repo, 20)
	path := filepath.Join(t.TempDir(), "document.md")
	d, err := service.Create(ctx, path, document.Draft{Body: "one two", Layout: document.DefaultLayout()})
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.SetTracking(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.Save(ctx, d, document.Draft{Body: "one bright two", Layout: d.Layout})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(ctx, path)
	if err != nil || !loaded.Tracking || len(loaded.TrackedChanges) != 1 || loaded.Body != d.Body {
		t.Fatalf("reopened tracking: %+v, %v", loaded, err)
	}
	resolved, err := service.ReviewTracked(ctx, loaded, loaded.TrackedChanges[0].ID, false)
	if err != nil || resolved.Body != "one two" || len(resolved.TrackedChanges) != 0 {
		t.Fatalf("reject: %+v, %v", resolved, err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatalf("undo rejection: %v", err)
	}
	loaded, err = service.Get(ctx, path)
	if err != nil || loaded.Body != "one bright two" || len(loaded.TrackedChanges) != 1 {
		t.Fatalf("undo did not restore tracking: %+v, %v", loaded, err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatalf("redo rejection: %v", err)
	}
	if _, err := service.ReviewTracked(ctx, loaded, loaded.TrackedChanges[0].ID, true); err == nil {
		t.Fatal("stale review overwrote a newer file")
	}
}

func TestDocumentRejectsInvalidTrackingMetadata(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repo, 20)
	path := filepath.Join(t.TempDir(), "document.md")
	if _, err := service.Create(ctx, path, document.Draft{Body: "before", Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModifyWithTracking(ctx, path, "edit", true, func(d *document.Document) error {
		d.Body = "after"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ old, replacement string }{
		{`"tracking_version": 1`, `"tracking_version": 2`},
		{`"inserted": "after"`, `"inserted": "other"`},
	} {
		t.Run(tt.old, func(t *testing.T) {
			changed := bytes.Replace(content, []byte(tt.old), []byte(tt.replacement), 1)
			if bytes.Equal(changed, content) {
				t.Fatal("metadata fixture did not match")
			}
			if _, err := decodeDocument(path, changed); err == nil {
				t.Fatal("invalid tracking metadata was accepted")
			}
		})
	}
}

func TestTrackedImageRemovalRetainsBytesUntilReview(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repo, 20)
	path := filepath.Join(t.TempDir(), "image.md")
	if _, err := service.Create(ctx, path, document.Draft{Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}
	d, name, err := service.AddImage(ctx, path, "image", tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	originalBody := d.Body
	d, err = service.SetTracking(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.RemoveImage(ctx, path, name, true)
	if err != nil || len(d.Images) != 1 || len(d.TrackedChanges) != 1 {
		t.Fatalf("tracked image removal lost bytes: %+v, %v", d, err)
	}
	d, err = service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	d, err = service.ReviewTracked(ctx, d, "", false)
	if err != nil || d.Body != originalBody || len(d.Images) != 1 || d.Images[0].Name != name {
		t.Fatalf("rejected removal: %+v, %v", d, err)
	}
	if _, err := service.ReviewTracked(ctx, d, "missing", true); !errors.Is(err, document.ErrChangeNotFound) {
		t.Fatalf("missing change: %v", err)
	}
}
