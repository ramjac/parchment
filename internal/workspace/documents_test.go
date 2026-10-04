package workspace

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
)

func testPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 3, 3))
	img.Pix[0] = shade
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocumentsPersistInspectablyAndCoexistWithNotes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	notes := note.NewService(ws, 10)

	layout := document.DefaultLayout()
	layout.Columns = 2
	layout.Header = "{title}"
	created, err := docs.Create(ctx, document.Draft{Title: "  Report ", Body: "# Report\n\nText", Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Create(ctx, "A note", "report text"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	for _, name := range []string{"content.md", "metadata.json", "layout.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", name, info.Mode().Perm())
		}
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "content.md")); string(body) != "# Report\n\nText" {
		t.Fatalf("content.md = %q", body)
	}
	if meta, _ := os.ReadFile(filepath.Join(dir, "metadata.json")); !strings.Contains(string(meta), `"kind": "document"`) {
		t.Fatalf("metadata = %s", meta)
	}

	got, err := docs.Get(ctx, created.ID)
	if err != nil || got.Title != "Report" || got.Layout.Columns != 2 || got.Layout.Header != "{title}" {
		t.Fatalf("got = %+v, %v", got, err)
	}
	listedDocs, _ := docs.List(ctx)
	listedNotes, _ := notes.List(ctx)
	if len(listedDocs) != 1 || len(listedNotes) != 1 || listedNotes[0].Title != "A note" {
		t.Fatalf("documents=%d notes=%d", len(listedDocs), len(listedNotes))
	}
	if _, err := notes.Get(ctx, created.ID); err == nil {
		t.Fatal("a document was readable as a note")
	}
	if _, err := docs.Get(ctx, listedNotes[0].ID); err == nil {
		t.Fatal("a note was readable as a document")
	}
	if err := notes.Delete(ctx, created.ID); err == nil {
		t.Fatal("note service deleted a document")
	}
}

func TestDocumentWithoutLayoutFileUsesDefaults(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Plain", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".parchment", "artifacts", created.ID, "layout.json")); err != nil {
		t.Fatal(err)
	}
	got, err := docs.Get(ctx, created.ID)
	if err != nil || got.Layout != document.DefaultLayout() {
		t.Fatalf("layout = %+v, %v", got.Layout, err)
	}
}

func TestDocumentImagesUndoRedoAndCleanup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Pictures", Body: "Intro"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)

	withImage, name, err := docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 200))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withImage.Body, "![Chart]("+name+")") || len(withImage.Images) != 1 {
		t.Fatalf("document after image = %+v", withImage)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	listed, _ := docs.List(ctx)
	if len(listed[0].Images) != 0 {
		t.Fatal("List loaded image data")
	}

	if _, err := docs.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatalf("image file remained after undo: %v", err)
	}
	if _, err := docs.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := docs.Get(ctx, created.ID)
	if err != nil || len(restored.Images) != 1 || !bytes.Equal(restored.Images[0].Data, withImage.Images[0].Data) {
		t.Fatalf("redo did not restore the image: %+v, %v", restored, err)
	}

	// Removing the reference prunes the stored image.
	if _, err := docs.Save(ctx, restored, document.Draft{Title: "Pictures", Body: "Intro", Layout: restored.Layout, Images: restored.Images}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced image was kept: %v", err)
	}

	// Deleting and undoing restores images too.
	again, _, err := docs.AddImage(ctx, created.ID, "", testPNG(t, 90))
	if err != nil {
		t.Fatal(err)
	}
	if err := docs.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("deleted document directory remains: %v", err)
	}
	if _, err := docs.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	back, err := docs.Get(ctx, created.ID)
	if err != nil || !document.Equal(back, again) {
		t.Fatalf("undo delete restored %+v, %v", back, err)
	}
}

func TestDocumentSaveDetectsConcurrentChange(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Shared", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := docs.Get(ctx, created.ID)
	if _, err := docs.Rename(ctx, created.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Save(ctx, stale, document.Draft{Title: "Shared", Body: "two", Layout: stale.Layout}); err == nil {
		t.Fatal("stale save overwrote a newer document")
	}
	current, _ := docs.Get(ctx, created.ID)
	if current.Title != "Renamed" || current.Body != "one" {
		t.Fatalf("document = %+v", current)
	}
}

func TestDocumentServiceValidatesInput(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	docs := document.NewService(ws, 10)
	if _, err := docs.Create(ctx, document.Draft{Title: "  "}); err == nil {
		t.Fatal("blank title was accepted")
	}
	bad := document.DefaultLayout()
	bad.Columns = 7
	if _, err := docs.Create(ctx, document.Draft{Title: "x", Layout: bad}); err == nil {
		t.Fatal("invalid layout was accepted")
	}
	created, err := docs.Create(ctx, document.Draft{Title: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.SetLayout(ctx, created.ID, bad); err == nil {
		t.Fatal("invalid layout update was accepted")
	}
	if _, _, err := docs.AddImage(ctx, created.ID, "", []byte("junk")); err == nil {
		t.Fatal("junk image was accepted")
	}
	if err := docs.AddTag(ctx, created.ID, "draft"); err != nil {
		t.Fatal(err)
	}
	if err := docs.AddTag(ctx, created.ID, "draft"); err != nil {
		t.Fatal(err)
	}
	got, _ := docs.Get(ctx, created.ID)
	if len(got.Tags) != 1 {
		t.Fatalf("tags = %v", got.Tags)
	}
	if err := docs.RemoveTag(ctx, created.ID, "draft"); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Get(ctx, strings.Repeat("0", 32)); err != document.ErrNotFound {
		t.Fatalf("missing document error = %v", err)
	}
}

func TestDocumentHistoryIsIndependentOfReturnedImageBytes(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Pictures"})
	if err != nil {
		t.Fatal(err)
	}
	withImage, _, err := docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 120))
	if err != nil {
		t.Fatal(err)
	}
	withImage.Images[0].Data[0] ^= 0xff
	if _, err := docs.Undo(ctx); err != nil {
		t.Fatalf("undo after mutating returned image bytes: %v", err)
	}
	if _, err := docs.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := docs.Get(ctx, created.ID)
	if err != nil || len(restored.Images) != 1 || restored.Images[0].Validate() != nil {
		t.Fatalf("redo restored modified image bytes: %+v, %v", restored, err)
	}
}

func TestCorruptOrRenamedWorkspaceImageIsRejected(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Pictures"})
	if err != nil {
		t.Fatal(err)
	}
	_, name, err := docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 30))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".parchment", "artifacts", created.ID, name)
	if err := os.WriteFile(path, testPNG(t, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Get(ctx, created.ID); err == nil {
		t.Fatal("image whose content does not match its name was loaded")
	}
}

func TestImageWithMarkdownTitleSurvivesUnrelatedEdits(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Pictures"})
	if err != nil {
		t.Fatal(err)
	}
	withImage, name, err := docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 60))
	if err != nil {
		t.Fatal(err)
	}
	body := "![Chart](" + name + ` "caption")`
	saved, err := docs.Save(ctx, withImage, document.Draft{Title: "Pictures", Body: body, Layout: withImage.Layout, Images: withImage.Images})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Rename(ctx, saved.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".parchment", "artifacts", created.ID, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("linked image was deleted: %v", err)
	}
}
