package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/search"
)

func TestWorkspacePersistsInspectableNotesAndStableIDs(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 10)
	created, err := service.Create(context.Background(), "Trip ideas", "Visit the museum")
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != "note" || created.FormatVersion != 1 || created.CreatedAt.Location().String() != "UTC" {
		t.Fatalf("unexpected artifact metadata: %+v", created.Artifact)
	}
	contentPath := filepath.Join(root, filepath.FromSlash(created.Location))
	content, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "Visit the museum" {
		t.Fatalf("content = %q", content)
	}
	if err := service.AddTag(context.Background(), created.ID, "travel"); err != nil {
		t.Fatal(err)
	}
	renamed, err := service.Rename(context.Background(), created.ID, "Weekend ideas")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != created.ID {
		t.Fatalf("rename changed artifact ID from %q to %q", created.ID, renamed.ID)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Weekend ideas" || loaded.Body != "Visit the museum" || len(loaded.Tags) != 1 {
		t.Fatalf("loaded note = %+v", loaded)
	}
	results, err := search.Notes(context.Background(), reopened, "TRAVEL")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != created.ID {
		t.Fatalf("search results = %+v", results)
	}
	if found, err := Find(root); err != nil || found != root {
		t.Fatalf("Find = %q, %v", found, err)
	}
}

func TestWorkspaceRejectsUnsafeIDsAndLocations(t *testing.T) {
	ws, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Get(context.Background(), "../outside"); err != note.ErrNotFound {
		t.Fatalf("unsafe ID error = %v, want ErrNotFound", err)
	}
}

func TestWorkspaceReportsMissingNoteContent(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := note.NewService(ws, 10).Create(context.Background(), "Incomplete note", "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(created.Location))); err != nil {
		t.Fatal(err)
	}

	if _, err := ws.List(context.Background()); err == nil {
		t.Fatal("list ignored note with missing content")
	}
	if _, err := ws.Get(context.Background(), created.ID); err == nil || errors.Is(err, note.ErrNotFound) {
		t.Fatalf("get error = %v, want missing-content error", err)
	}
}

func TestSaveFailurePreservesExistingArtifact(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 10)
	created, err := service.Create(ctx, "Existing", "original body")
	if err != nil {
		t.Fatal(err)
	}

	invalid := created
	invalid.Body = "replacement body"
	invalid.ModifiedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := ws.Save(ctx, invalid); err == nil {
		t.Fatal("save succeeded with an unrepresentable timestamp")
	}
	loaded, err := ws.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Body != created.Body || !loaded.ModifiedAt.Equal(created.ModifiedAt) {
		t.Fatalf("failed save changed artifact: %+v", loaded)
	}

	metadataPath := filepath.Join(root, ".parchment", "artifacts", created.ID, metadataName)
	if err := os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadataPath, 0o700); err != nil {
		t.Fatal(err)
	}
	invalid = created
	invalid.Body = "second replacement"
	if err := ws.Save(ctx, invalid); err == nil {
		t.Fatal("save succeeded when metadata destination was a directory")
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(created.Location)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != created.Body {
		t.Fatalf("failed save changed content to %q", content)
	}
}

func TestFailedCreateDoesNotLeaveArtifactDirectory(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	n := note.Note{Artifact: artifact.Artifact{
		ID: "0123456789abcdef0123456789abcdef", Kind: artifact.NoteKind, Title: "Invalid time",
		CreatedAt:  time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC),
		ModifiedAt: time.Now().UTC(), FormatVersion: artifact.FormatVersion,
		Location: ".parchment/artifacts/0123456789abcdef0123456789abcdef/content.md",
	}}
	if err := ws.Save(context.Background(), n); err == nil {
		t.Fatal("save succeeded with an unrepresentable timestamp")
	}
	dir := filepath.Join(root, ".parchment", "artifacts", n.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("failed create left artifact directory: %v", err)
	}
}

func TestNoteRepositoryIgnoresOtherArtifactKinds(t *testing.T) {
	ws, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(ws.Root(), ".parchment", "artifacts", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := artifact.Artifact{
		ID: id, Kind: artifact.DocumentKind, Title: "Future document",
		CreatedAt:     time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ModifiedAt:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		FormatVersion: artifact.FormatVersion, Location: "documents/" + id + ".md",
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, metadataName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	notes, err := ws.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("note list included non-note artifacts: %+v", notes)
	}
}
