package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifactfile"
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
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "content.md" {
		t.Fatalf("document artifact files = %v, %v", entries, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), "```parchment-meta\n") ||
		!strings.Contains(string(content), "```parchment-document\n") ||
		!strings.Contains(string(content), "# Report\n\nText") {
		t.Fatalf("content.md does not contain the complete document: %s", content)
	}
	if info, err := os.Stat(filepath.Join(dir, "content.md")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("document file permissions = %v, %v", info, err)
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

func TestDocumentPersistsDefaultLayoutInMarkdownArtifact(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Plain", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := docs.Get(ctx, created.ID)
	if err != nil || got.Layout != document.DefaultLayout() {
		t.Fatalf("layout = %+v, %v", got.Layout, err)
	}
}

func TestListDocumentsDoesNotDecodeEmbeddedImages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Images", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".parchment", "artifacts", created.ID, "content.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(file.Blocks[documentDataBlock], &payload); err != nil {
		t.Fatal(err)
	}
	payload["images"] = json.RawMessage(`[{"name":"broken.png","data":"!"}]`)
	file.Blocks[documentDataBlock], err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	blocks := make(map[string]any, len(file.Blocks))
	for name, block := range file.Blocks {
		blocks[name] = block
	}
	data, err = artifactfile.Encode(file.Artifact, file.Body, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	listed, err := service.List(ctx)
	if err != nil || len(listed) != 1 || len(listed[0].Images) != 0 {
		t.Fatalf("listed documents = %+v, %v", listed, err)
	}
	if _, err := ws.readDocumentChangesUnlocked(created.ID); err != nil {
		t.Fatalf("reading document changes decoded embedded image data: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("Get accepted invalid embedded image data")
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
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "content.md" {
		t.Fatalf("image was not embedded in the Markdown artifact: %v, %v", entries, err)
	}
	listed, _ := docs.List(ctx)
	if len(listed[0].Images) != 0 {
		t.Fatal("List loaded image data")
	}

	if _, err := docs.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	undone, err := docs.Get(ctx, created.ID)
	if err != nil || len(undone.Images) != 0 {
		t.Fatalf("image remained after undo: %+v, %v", undone.Images, err)
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

func TestDocumentChangesPersistAndResolveAtomically(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Draft", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}

	proposal, err := service.Propose(ctx, created.ID, "Revise text", document.Draft{
		Title: "Revised", Body: "proposed", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := service.Get(ctx, created.ID)
	if err != nil || live.Title != "Draft" || live.Body != "original" {
		t.Fatalf("proposal changed the live document: %+v, %v", live, err)
	}
	if _, err := service.Propose(ctx, created.ID, "Another", document.Draft{
		Title: "Another", Body: "other", Layout: created.Layout,
	}); err == nil {
		t.Fatal("a second pending proposal was accepted")
	}

	changeData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(created.Location)))
	if err != nil || !strings.Contains(string(changeData), `"body": "proposed"`) {
		t.Fatalf("proposal was not stored in the Markdown artifact: %s, %v", changeData, err)
	}
	reopened := document.NewService(ws, 10)
	persisted, err := reopened.GetChange(ctx, created.ID, proposal.ID)
	if err != nil || persisted.Status != document.ChangePending || persisted.After.Title != "Revised" {
		t.Fatalf("persisted proposal = %+v, %v", persisted, err)
	}
	if err := reopened.Reject(ctx, created.ID, proposal.ID); err != nil {
		t.Fatal(err)
	}
	rejected, err := reopened.GetChange(ctx, created.ID, proposal.ID)
	if err != nil || rejected.Status != document.ChangeRejected || rejected.ResolvedAt == nil {
		t.Fatalf("rejected proposal = %+v, %v", rejected, err)
	}
	live, err = reopened.Get(ctx, created.ID)
	if err != nil || live.Title != "Draft" || live.Body != "original" {
		t.Fatalf("reject changed the live document: %+v, %v", live, err)
	}

	proposal, err = reopened.Propose(ctx, created.ID, "Accept text", document.Draft{
		Title: "Accepted", Body: "final", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepting := document.NewService(ws, 10)
	accepted, err := accepting.Accept(ctx, created.ID, proposal.ID)
	if err != nil || accepted.Title != "Accepted" || accepted.Body != "final" {
		t.Fatalf("accept result = %+v, %v", accepted, err)
	}
	status, err := accepting.GetChange(ctx, created.ID, proposal.ID)
	if err != nil || status.Status != document.ChangeAccepted || status.ResolvedAt == nil {
		t.Fatalf("accepted proposal = %+v, %v", status, err)
	}
	if _, err := accepting.Undo(ctx); err != nil {
		t.Fatalf("undo accepted proposal: %v", err)
	}
	undone, err := accepting.Get(ctx, created.ID)
	if err != nil || undone.Title != "Draft" || undone.Body != "original" {
		t.Fatalf("document after undo = %+v, %v", undone, err)
	}
	if _, err := accepting.Redo(ctx); err != nil {
		t.Fatalf("redo accepted proposal: %v", err)
	}
	redone, err := accepting.Get(ctx, created.ID)
	if err != nil || redone.Title != "Accepted" || redone.Body != "final" {
		t.Fatalf("document after redo = %+v, %v", redone, err)
	}
}

func TestAcceptDocumentChangeRejectsStaleProposal(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Shared", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := service.Propose(ctx, created.ID, "Proposed edit", document.Draft{
		Title: "Proposed", Body: "proposed", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Rename(ctx, created.ID, "Immediate edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Accept(ctx, created.ID, proposal.ID); err == nil {
		t.Fatal("stale proposal overwrote a newer document")
	}
	current, err := service.Get(ctx, created.ID)
	if err != nil || current.Title != "Immediate edit" || current.Body != "original" {
		t.Fatalf("document after stale proposal = %+v, %v", current, err)
	}
}

func TestDocumentProposalCannotAddEmbeddedImageData(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Image proposal", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	image, err := document.NewImage(testPNG(t, 44))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Propose(ctx, created.ID, "Add image", document.Draft{
		Title: created.Title, Body: document.ImageMarkdown("chart", image.Name),
		Layout: created.Layout, Images: []document.Image{image},
	}); err == nil {
		t.Fatal("proposal accepted new embedded image data")
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
	_, _, err = docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 30))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(created.Location))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(content)
	if err != nil {
		t.Fatal(err)
	}
	var payload documentFileData
	if err := json.Unmarshal(file.Blocks[documentDataBlock], &payload); err != nil {
		t.Fatal(err)
	}
	payload.Images[0].Data = testPNG(t, 31)
	content, err = artifactfile.Encode(file.Artifact, file.Body, map[string]any{documentDataBlock: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
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
	persisted, err := docs.Get(ctx, created.ID)
	if err != nil || len(persisted.Images) != 1 || persisted.Images[0].Name != name {
		t.Fatalf("linked image was not preserved: %+v, %v", persisted.Images, err)
	}
}
