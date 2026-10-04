package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
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

func TestLegacyDocumentExampleMetadataDoesNotReplaceSidecar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Legacy document", Body: "Original body"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	example := created.Artifact
	example.ID = "abcdef0123456789abcdef0123456789"
	example.Location = filepath.ToSlash(filepath.Join(".parchment", "artifacts", example.ID, "content.md"))
	example.Title = "Embedded example"
	metadata, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	exampleBody := "```parchment-meta\n" + string(metadata) + "\n```\n\n" +
		"<!-- parchment-body -->\n# Example document\n\n## Slide\n\nExample body\n"
	sidecar, err := json.Marshal(created.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := json.Marshal(created.Layout)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"content.md": []byte(exampleBody), "metadata.json": sidecar, "layout.json": layout,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := docs.Get(ctx, created.ID)
	if err != nil || loaded.Title != created.Title || loaded.Body != exampleBody {
		t.Fatalf("legacy document with embedded example = %+v, %v", loaded, err)
	}
	listed, err := docs.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Body != exampleBody {
		t.Fatalf("legacy document list with embedded example = %+v, %v", listed, err)
	}
	markedExample, err := artifactfile.Encode(example, "marked example body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.md"), markedExample, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Get(ctx, created.ID); err == nil {
		t.Fatal("marked embedded metadata fell back to the legacy sidecar")
	}
}

func TestSavingLegacyDocumentRemovesManagedSidecarsAndImages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Legacy document", Body: "Old body"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	image, err := document.NewImage(testPNG(t, 77))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(created.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := json.Marshal(created.Layout)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"content.md": []byte("Old body"), "metadata.json": metadata,
		"layout.json": layout, image.Name: image.Data,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := docs.Get(ctx, created.ID)
	if err != nil || len(loaded.Images) != 1 {
		t.Fatalf("legacy document image = %+v, %v", loaded.Images, err)
	}
	if _, err := docs.Save(ctx, loaded, document.Draft{
		Title: loaded.Title, Body: "Updated body", Layout: loaded.Layout,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"metadata.json", "layout.json", image.Name} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("legacy sidecar %q remains after save: %v", name, err)
		}
	}
}

func TestDocumentCleanupFailureDoesNotFailCommittedSave(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	docs := document.NewService(ws, 10)
	created, err := docs.Create(ctx, document.Draft{Title: "Committed save", Body: "Before"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	layoutPath := filepath.Join(dir, "layout.json")
	if err := os.Mkdir(layoutPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layoutPath, "unmanaged"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Save(ctx, created, document.Draft{
		Title: created.Title, Body: "After", Layout: created.Layout,
	}); err != nil {
		t.Fatalf("committed save reported cleanup failure: %v", err)
	}
	loaded, err := docs.Get(ctx, created.ID)
	if err != nil || loaded.Body != "After" {
		t.Fatalf("committed document = %+v, %v", loaded, err)
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

	proposal, err := service.Propose(ctx, created, "Revise text", document.Draft{
		Title: "Revised", Body: "proposed", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := service.Get(ctx, created.ID)
	if err != nil || live.Title != "Draft" || live.Body != "original" {
		t.Fatalf("proposal changed the live document: %+v, %v", live, err)
	}
	if _, err := service.Propose(ctx, created, "Another", document.Draft{
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

	proposal, err = reopened.Propose(ctx, created, "Accept text", document.Draft{
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

func TestUndoDocumentDeleteRestoresChangeHistory(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Tracked", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := service.Propose(ctx, created, "Rejected edit", document.Draft{
		Title: "Rejected", Body: "first", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reject(ctx, created.ID, rejected.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Propose(ctx, created, "Pending edit", document.Draft{
		Title: "Pending", Body: "second", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	changes, err := service.Changes(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("restored %d change records, want 2: %+v", len(changes), changes)
	}
	statuses := map[string]document.ChangeStatus{}
	for _, change := range changes {
		statuses[change.ID] = change.Status
	}
	if statuses[rejected.ID] != document.ChangeRejected || statuses[pending.ID] != document.ChangePending {
		t.Fatalf("restored change statuses = %v", statuses)
	}
}

func TestUndoRedoDocumentCreationPreservesChangeHistory(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Tracked", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := service.Propose(ctx, created, "Proposed edit", document.Draft{
		Title: "Updated", Body: "proposed", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	changes, err := service.Changes(ctx, created.ID)
	if err != nil || len(changes) != 1 || changes[0].ID != proposal.ID || changes[0].Status != document.ChangePending {
		t.Fatalf("changes after undo and redo = %+v, %v", changes, err)
	}
}

func TestLegacyNoteAndDocumentArtifactsRemainReadable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	notes := note.NewService(ws, 10)
	docs := document.NewService(ws, 10)
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	legacyNoteBody := "```parchment-footnote\nnot JSON\n```\n\nPlain Markdown note"
	legacyNote, err := notes.Create(ctx, "Legacy note", legacyNoteBody)
	if err != nil {
		t.Fatal(err)
	}
	noteDir := filepath.Join(root, ".parchment", "artifacts", legacyNote.ID)
	noteMetadata, err := json.Marshal(legacyNote.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(noteDir, "metadata.json"), noteMetadata)
	write(filepath.Join(noteDir, "content.md"), []byte(legacyNote.Body))

	layout := document.DefaultLayout()
	layout.Columns = 2
	legacyDocumentBody := "```parchment-footnote\nnot JSON\n```\n\nIntroduction"
	created, err := docs.Create(ctx, document.Draft{Title: "Legacy document", Body: legacyDocumentBody, Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	withImage, imageName, err := docs.AddImage(ctx, created.ID, "Chart", testPNG(t, 88))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := docs.Propose(ctx, withImage, "Legacy proposal", document.Draft{
		Title: withImage.Title, Body: withImage.Body + "\nProposed text", Layout: withImage.Layout,
		Images: withImage.Images,
	})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := docs.Changes(ctx, created.ID)
	if err != nil || len(changes) != 1 || changes[0].ID != proposal.ID {
		t.Fatalf("proposal changes = %+v, %v", changes, err)
	}
	docDir := filepath.Join(root, ".parchment", "artifacts", withImage.ID)
	docMetadata, err := json.Marshal(withImage.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	layoutData, err := json.Marshal(withImage.Layout)
	if err != nil {
		t.Fatal(err)
	}
	changeData, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(docDir, "metadata.json"), docMetadata)
	write(filepath.Join(docDir, "layout.json"), layoutData)
	write(filepath.Join(docDir, "changes.json"), changeData)
	write(filepath.Join(docDir, "content.md"), []byte(withImage.Body))
	write(filepath.Join(docDir, imageName), withImage.Images[0].Data)

	legacyDefault, err := docs.Create(ctx, document.Draft{Title: "Legacy default", Body: "Text"})
	if err != nil {
		t.Fatal(err)
	}
	defaultDir := filepath.Join(root, ".parchment", "artifacts", legacyDefault.ID)
	defaultMetadata, err := json.Marshal(legacyDefault.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(defaultDir, "metadata.json"), defaultMetadata)
	write(filepath.Join(defaultDir, "content.md"), []byte(legacyDefault.Body))

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	loadedNotes, err := note.NewService(reopened, 10).List(ctx)
	if err != nil || len(loadedNotes) != 1 || loadedNotes[0].Body != legacyNoteBody {
		t.Fatalf("legacy notes = %+v, %v", loadedNotes, err)
	}
	loadedDocs := document.NewService(reopened, 10)
	loaded, err := loadedDocs.Get(ctx, withImage.ID)
	if err != nil || loaded.Body != withImage.Body || loaded.Layout != layout ||
		len(loaded.Images) != 1 || loaded.Images[0].Name != imageName {
		t.Fatalf("legacy document = %+v, %v", loaded, err)
	}
	loadedChanges, err := loadedDocs.Changes(ctx, withImage.ID)
	if err != nil || len(loadedChanges) != 1 || loadedChanges[0].ID != proposal.ID {
		t.Fatalf("legacy document changes = %+v, %v", loadedChanges, err)
	}
	defaultDocument, err := loadedDocs.Get(ctx, legacyDefault.ID)
	if err != nil || defaultDocument.Layout != document.DefaultLayout() {
		t.Fatalf("legacy document default layout = %+v, %v", defaultDocument.Layout, err)
	}
	deckService := presentation.NewService(reopened, 10)
	if _, err := deckService.Create(ctx, "Current presentation", "# Current presentation\n\n## Slide\n\nBody"); err != nil {
		t.Fatal(err)
	}
	decks, err := deckService.List(ctx)
	if err != nil || len(decks) != 1 {
		t.Fatalf("presentations alongside legacy artifacts = %+v, %v", decks, err)
	}
	spreadsheetService := spreadsheet.NewService(reopened, 10)
	if _, err := spreadsheetService.Create(ctx, "Current spreadsheet", [][]spreadsheet.Cell{{{Value: "value"}}}); err != nil {
		t.Fatal(err)
	}
	workbooks, err := spreadsheetService.List(ctx)
	if err != nil || len(workbooks) != 1 {
		t.Fatalf("spreadsheets alongside legacy artifacts = %+v, %v", workbooks, err)
	}
}

func TestInvalidEmbeddedMetadataDoesNotFallBackToLegacySidecars(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	corruptMetadata := func(path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		corrupted := strings.Replace(string(data), `"title": "Current`, `"title": 42`, 1)
		if corrupted == string(data) {
			t.Fatal("embedded metadata title was not found")
		}
		write(path, []byte(corrupted))
	}

	notes := note.NewService(ws, 10)
	legacyNote, err := notes.Create(ctx, "Legacy note", "Plain Markdown note")
	if err != nil {
		t.Fatal(err)
	}
	noteDir := filepath.Join(root, ".parchment", "artifacts", legacyNote.ID)
	noteMetadata, err := json.Marshal(legacyNote.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(noteDir, "metadata.json"), noteMetadata)
	write(filepath.Join(noteDir, "content.md"), []byte(legacyNote.Body))
	if _, err := notes.Rename(ctx, legacyNote.ID, "Current note"); err != nil {
		t.Fatal(err)
	}
	noteContentPath := filepath.Join(noteDir, "content.md")
	if _, err := os.Stat(filepath.Join(noteDir, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy note metadata sidecar remains after save: %v", err)
	}
	noteContent, err := os.ReadFile(noteContentPath)
	if err != nil {
		t.Fatal(err)
	}
	write(noteContentPath, append([]byte("\n"), noteContent...))
	migratedNote, err := notes.Get(ctx, legacyNote.ID)
	if err != nil || migratedNote.Title != "Current note" || migratedNote.Body != legacyNote.Body {
		t.Fatalf("migrated note after leading blank line = %+v, %v", migratedNote, err)
	}
	write(filepath.Join(noteDir, "metadata.json"), noteMetadata)
	corruptMetadata(noteContentPath)
	if _, err := notes.Get(ctx, legacyNote.ID); err == nil {
		t.Fatal("malformed note metadata fell back to the stale sidecar")
	}

	documents := document.NewService(ws, 10)
	layout := document.DefaultLayout()
	layout.Columns = 2
	legacyDocument, err := documents.Create(ctx, document.Draft{
		Title: "Legacy document", Body: "Plain Markdown", Layout: layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := documents.Propose(ctx, legacyDocument, "Existing proposal", document.Draft{
		Title: legacyDocument.Title, Body: legacyDocument.Body + " proposed", Layout: legacyDocument.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	documentDir := filepath.Join(root, ".parchment", "artifacts", legacyDocument.ID)
	documentMetadata, err := json.Marshal(legacyDocument.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	layoutData, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	changeData, err := json.Marshal([]document.Change{proposal})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(documentDir, "metadata.json"), documentMetadata)
	write(filepath.Join(documentDir, "layout.json"), layoutData)
	write(filepath.Join(documentDir, "changes.json"), changeData)
	write(filepath.Join(documentDir, "content.md"), []byte(legacyDocument.Body))
	if _, err := documents.Rename(ctx, legacyDocument.ID, "Current document"); err != nil {
		t.Fatal(err)
	}
	staleLayout := document.DefaultLayout()
	staleLayout.Columns = 3
	staleLayoutData, err := json.Marshal(staleLayout)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(documentDir, "layout.json"), staleLayoutData)
	write(filepath.Join(documentDir, "changes.json"), []byte("[]"))
	documentContentPath := filepath.Join(documentDir, "content.md")
	documentContent, err := os.ReadFile(documentContentPath)
	if err != nil {
		t.Fatal(err)
	}
	write(documentContentPath, append([]byte("\n"), documentContent...))
	migratedDocument, err := documents.Get(ctx, legacyDocument.ID)
	if err != nil || migratedDocument.Title != "Current document" || migratedDocument.Body != legacyDocument.Body ||
		migratedDocument.Layout != layout {
		t.Fatalf("migrated document after leading blank line = %+v, %v", migratedDocument, err)
	}
	migratedChanges, err := documents.Changes(ctx, legacyDocument.ID)
	if err != nil || len(migratedChanges) != 1 || migratedChanges[0].ID != proposal.ID {
		t.Fatalf("migrated proposal history after leading blank line = %+v, %v", migratedChanges, err)
	}
	corruptMetadata(documentContentPath)
	if _, err := documents.Get(ctx, legacyDocument.ID); err == nil {
		t.Fatal("malformed document metadata fell back to the stale sidecar")
	}
}

func TestMissingContentForKnownLegacyArtifactsIsReported(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)

	legacyNote, err := note.NewService(ws, 10).Create(ctx, "Missing note body", "body")
	if err != nil {
		t.Fatal(err)
	}
	noteDir := filepath.Join(root, ".parchment", "artifacts", legacyNote.ID)
	noteMetadata, err := json.Marshal(legacyNote.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noteDir, "metadata.json"), noteMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(noteDir, "content.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Get(ctx, legacyNote.ID); err == nil || errors.Is(err, note.ErrNotFound) {
		t.Fatalf("missing content for known note returned %v", err)
	}

	legacyDocument, err := document.NewService(ws, 10).Create(ctx, document.Draft{Title: "Missing document body", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	documentDir := filepath.Join(root, ".parchment", "artifacts", legacyDocument.ID)
	documentMetadata, err := json.Marshal(legacyDocument.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(documentDir, "metadata.json"), documentMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(documentDir, "content.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.GetDocument(ctx, legacyDocument.ID); err == nil || errors.Is(err, document.ErrNotFound) {
		t.Fatalf("missing content for known document returned %v", err)
	}
}

func TestProposeRejectsAStaleDocumentSnapshot(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	service := document.NewService(ws, 10)
	created, err := service.Create(ctx, document.Draft{Title: "Before", Body: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Rename(ctx, created.ID, "Concurrent edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Propose(ctx, created, "Stale edit", document.Draft{
		Title: "Stale proposal", Body: "stale", Layout: created.Layout,
	}); err == nil {
		t.Fatal("stale document snapshot was accepted as a proposal baseline")
	}
	current, err := service.Get(ctx, created.ID)
	if err != nil || current.Title != "Concurrent edit" || current.Body != "original" {
		t.Fatalf("stale proposal changed the document: %+v, %v", current, err)
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
	proposal, err := service.Propose(ctx, created, "Proposed edit", document.Draft{
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
	if _, err := service.Propose(ctx, created, "Add image", document.Draft{
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
