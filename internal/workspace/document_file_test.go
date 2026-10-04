package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

const standaloneDocumentExtraBlock = "parchment-review-notes"

func standaloneDocumentPath(t *testing.T) string {
	t.Helper()
	sample, err := os.ReadFile("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n```" + standaloneDocumentExtraBlock + "\n{\"reviewer\":\"sam\"}\n```\n"
	sample = append(sample, []byte(extra)...)
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func reopenStandaloneDocument(t *testing.T, path string) (document.Document, []document.Change) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, changes, err := decodeDocumentContent(data, true, standaloneID(artifact.DocumentKind, path))
	if err != nil {
		t.Fatal(err)
	}
	d.Title = standaloneTitle(path)
	d.Location = filepath.ToSlash(path)
	return d, changes
}

func TestStandaloneDocumentEditsWithoutWorkspace(t *testing.T) {
	ctx := context.Background()
	path := standaloneDocumentPath(t)
	repository, err := OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repository, 10)
	listed, err := service.List(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %v, %v", listed, err)
	}
	id := listed[0].ID
	original, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Images) == 0 || repository.ArtifactLocation(id) != original.Location ||
		repository.ArtifactLocation(id+"x") != "" {
		t.Fatalf("unexpected document: %+v", original.Artifact)
	}
	if _, ok := original.Blocks[standaloneDocumentExtraBlock]; !ok {
		t.Fatalf("unknown block was not loaded: %v", original.Blocks)
	}
	listed, err = service.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("list = %v, %v", listed, err)
	}
	draft := document.Draft{
		Title: "Transient title must not replace filename", Body: original.Body + "\nEdited directly.\n",
		Layout: original.Layout, Images: original.Images,
	}
	if _, err := service.Save(ctx, original, draft); err != nil {
		t.Fatal(err)
	}
	saved, _ := reopenStandaloneDocument(t, path)
	if saved.Title != "brief" || saved.Body != draft.Body || saved.Location != original.Location ||
		saved.Layout != original.Layout || len(saved.Images) != len(original.Images) ||
		!bytes.Equal(saved.Images[0].Data, original.Images[0].Data) ||
		!bytes.Equal(saved.Blocks[standaloneDocumentExtraBlock], json.RawMessage(`{"reviewer":"sam"}`)) {
		t.Fatalf("save lost document data: %+v", saved)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v", info.Mode().Perm())
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, _ := reopenStandaloneDocument(t, path)
	if !document.Equal(restored, original) {
		t.Fatalf("undo did not restore document: %+v", restored.Artifact)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	if redone, _ := reopenStandaloneDocument(t, path); redone.Body != draft.Body {
		t.Fatalf("redo did not persist: %q", redone.Body)
	}
}

func TestStandaloneDocumentChangeProposals(t *testing.T) {
	ctx := context.Background()
	path := standaloneDocumentPath(t)
	repository, err := OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repository, 10)
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	id := items[0].ID
	before, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	draft := document.Draft{Title: before.Title, Body: before.Body + "\nProposed paragraph.\n", Layout: before.Layout, Images: before.Images}
	rejected, err := service.Propose(ctx, before, "Add paragraph", draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Propose(ctx, before, "Another", draft); err == nil {
		t.Fatal("second pending proposal was accepted")
	}
	if err := service.Reject(ctx, id, rejected.ID); err != nil {
		t.Fatal(err)
	}
	accepted, err := service.Propose(ctx, before, "Add paragraph again", draft)
	if err != nil {
		t.Fatal(err)
	}
	after, err := service.Accept(ctx, id, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, changes := reopenStandaloneDocument(t, path)
	if saved.Body != draft.Body || len(saved.Images) != len(before.Images) ||
		saved.Blocks[standaloneDocumentExtraBlock] == nil {
		t.Fatalf("accepted document lost data: %+v", saved)
	}
	if len(changes) != 2 || changes[0].Status != document.ChangeRejected || changes[1].Status != document.ChangeAccepted {
		t.Fatalf("persisted changes = %+v", changes)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	undone, changes := reopenStandaloneDocument(t, path)
	if !document.Equal(undone, before) || len(changes) != 2 {
		t.Fatalf("undo acceptance = %+v, %+v", undone.Artifact, changes)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	if redone, _ := reopenStandaloneDocument(t, path); !document.Equal(redone, after) {
		t.Fatal("redo acceptance did not persist")
	}

	reopened, err := OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := reopened.ListDocumentChanges(ctx, id)
	if err != nil || len(listed) != 2 {
		t.Fatalf("reopened changes = %v, %v", listed, err)
	}
}

func TestStandaloneDocumentRejectsExternalChangesAndUnsafeOperations(t *testing.T) {
	ctx := context.Background()
	path := standaloneDocumentPath(t)
	repository, err := OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repository, 10)
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	id := items[0].ID
	if err := service.Delete(ctx, id); err == nil || !strings.Contains(err.Error(), "cannot delete") {
		t.Fatalf("delete error = %v", err)
	}
	if _, err := service.Create(ctx, document.Draft{Title: "New", Body: "body"}); err == nil {
		t.Fatal("create succeeded in standalone document")
	}
	d, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.TransitionDocument(ctx, id, &d, nil); err == nil {
		t.Fatal("nil target was accepted")
	}
	moved := d
	moved.Location = "elsewhere.md"
	if err := repository.TransitionDocument(ctx, id, &d, &moved); err == nil {
		t.Fatal("location change was accepted")
	}
	if _, err := service.Get(ctx, id+"x"); err != document.ErrNotFound {
		t.Fatalf("missing ID error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := bytes.Replace(data, []byte("Community Garden"), []byte("Edited Garden"), 1)
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, d, document.Draft{
		Title: d.Title, Body: d.Body + "\nOverwrite\n", Layout: d.Layout, Images: d.Images,
	}); err == nil || !strings.Contains(err.Error(), "outside Parchment") {
		t.Fatalf("external edit error = %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, external) {
		t.Fatal("external edit was overwritten")
	}
}

func TestOpenDocumentFileRejectsOtherArtifacts(t *testing.T) {
	if _, err := OpenDocumentFile("../../examples/note.md"); err == nil || !strings.Contains(err.Error(), "not a Parchment document") {
		t.Fatalf("open note error = %v", err)
	}
	if _, err := OpenDocumentFile(t.TempDir()); err == nil {
		t.Fatal("opened a directory")
	}
}
