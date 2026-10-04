package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/search"
	"example.com/parchment/internal/spreadsheet"
)

const testExtensionBlock = "parchment-extension"

func openTestWorkspace(t *testing.T, root string) *Workspace {
	t.Helper()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func addTestPayloadBlock(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	blocks := make(map[string]any, len(file.Blocks)+1)
	for name, payload := range file.Blocks {
		blocks[name] = payload
	}
	blocks[testExtensionBlock] = json.RawMessage(`{"value":"preserved"}`)
	data, err = artifactfile.Encode(file.Artifact, file.Body, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireTestPayloadBlock(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	var block struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(file.Blocks[testExtensionBlock], &block); err != nil {
		t.Fatal(err)
	}
	if block.Value != "preserved" {
		t.Fatalf("extension block = %s", file.Blocks[testExtensionBlock])
	}
}

func TestArtifactEditsPreserveAdditionalPayloadBlocks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	contentPath := func(id string) string {
		return filepath.Join(root, ".parchment", "artifacts", id, "content.md")
	}

	notes := note.NewService(ws, 10)
	n, err := notes.Create(ctx, "Note", "body")
	if err != nil {
		t.Fatal(err)
	}
	addTestPayloadBlock(t, contentPath(n.ID))
	if _, err := notes.Rename(ctx, n.ID, "Renamed note"); err != nil {
		t.Fatal(err)
	}
	requireTestPayloadBlock(t, contentPath(n.ID))

	documents := document.NewService(ws, 10)
	d, err := documents.Create(ctx, document.Draft{Title: "Document", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	addTestPayloadBlock(t, contentPath(d.ID))
	if _, err := documents.Rename(ctx, d.ID, "Renamed document"); err != nil {
		t.Fatal(err)
	}
	requireTestPayloadBlock(t, contentPath(d.ID))

	sheets := spreadsheet.NewService(ws, 10)
	book, err := sheets.Create(ctx, "Workbook", [][]spreadsheet.Cell{{{Value: "before"}}})
	if err != nil {
		t.Fatal(err)
	}
	addTestPayloadBlock(t, contentPath(book.ID))
	if _, err := sheets.SetCell(ctx, book.ID, 1, 1, spreadsheet.Cell{Value: "after"}); err != nil {
		t.Fatal(err)
	}
	requireTestPayloadBlock(t, contentPath(book.ID))

	decks := presentation.NewService(ws, 10)
	deck, err := decks.Create(ctx, "Presentation", "# Presentation\n\n## Slide 1\n\nbefore")
	if err != nil {
		t.Fatal(err)
	}
	addTestPayloadBlock(t, contentPath(deck.ID))
	current, err := decks.Get(ctx, deck.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decks.Update(ctx, current, "# Presentation\n\n## Slide 1\n\nafter"); err != nil {
		t.Fatal(err)
	}
	requireTestPayloadBlock(t, contentPath(deck.ID))
}

func TestArtifactListsSkipOversizedArtifactsOfOtherKinds(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	n, err := note.NewService(ws, 1).Create(ctx, "Large note", "body")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".parchment", "artifacts", n.ID, "content.md")
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(artifactfile.MaxFileSize + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if items, err := ws.ListDocuments(ctx); err != nil || len(items) != 0 {
		t.Fatalf("documents = %d, %v", len(items), err)
	}
	if items, err := ws.ListPresentations(ctx); err != nil || len(items) != 0 {
		t.Fatalf("presentations = %d, %v", len(items), err)
	}
	if items, err := ws.ListSpreadsheets(ctx); err != nil || len(items) != 0 {
		t.Fatalf("spreadsheets = %d, %v", len(items), err)
	}
}

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
	artifactDir := filepath.Dir(contentPath)
	entries, err := os.ReadDir(artifactDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "content.md" {
		t.Fatalf("note artifact files = %v, %v", entries, err)
	}
	content, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(content)
	if err != nil || file.Artifact.Title != "Trip ideas" || file.Body != "Visit the museum" {
		t.Fatalf("decoded artifact file = %+v, %v", file, err)
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
	ws := openTestWorkspace(t, t.TempDir())
	if _, err := ws.Get(context.Background(), "../outside"); err != note.ErrNotFound {
		t.Fatalf("unsafe ID error = %v, want ErrNotFound", err)
	}
}

func TestFindSkipsArtifactDirectoriesWithoutWorkspaceMarker(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "workspace")
	if err := Init(parent); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(parent, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".parchment", "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	found, err := Find(nested)
	if err != nil {
		t.Fatal(err)
	}
	if found != parent {
		t.Fatalf("Find = %q, want initialized parent %q", found, parent)
	}
}

type cancelOnErrContext struct {
	context.Context
	cancelAt int
	calls    int
}

func (c *cancelOnErrContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestDeleteCancellationRollsBackStagedDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Keep", "body")
	if err != nil {
		t.Fatal(err)
	}
	artifactsDir := filepath.Join(root, ".parchment", "artifacts")
	cancelCtx := &cancelOnErrContext{Context: ctx, cancelAt: 4}

	err = ws.deleteLocked(cancelCtx, artifactsDir, created.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("delete error = %v, want cancellation", err)
	}
	if _, err := ws.Get(ctx, created.ID); err != nil {
		t.Fatalf("cancelled delete removed note: %v", err)
	}
	for _, name := range []string{pendingArtifactPrefix + created.ID, deleteIntentPrefix + created.ID} {
		if _, err := os.Lstat(filepath.Join(artifactsDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled deletion left %s: %v", name, err)
		}
	}
}

func TestDeleteRecoversCommittedIntentBeforeRedoingDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Redo", "body")
	if err != nil {
		t.Fatal(err)
	}
	artifactsDir := filepath.Join(root, ".parchment", "artifacts")
	tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+created.ID)
	if err := os.Rename(filepath.Join(artifactsDir, created.ID), tombstone); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactDeletionIntent(artifactsDir, artifactDeletionIntent{ID: created.ID, State: "committed"}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Save(ctx, created); err != nil {
		t.Fatalf("restore note as if undo had recreated it: %v", err)
	}

	if err := ws.deleteLocked(ctx, artifactsDir, created.ID); err != nil {
		t.Fatalf("redo deletion did not recover previous committed intent: %v", err)
	}
	if _, err := ws.Get(ctx, created.ID); !errors.Is(err, note.ErrNotFound) {
		t.Fatalf("note after redo deletion = %v, want not found", err)
	}
}

func TestInitRejectsNonRegularWorkspaceMarker(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "parchment.toml"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Init(root); err == nil {
			t.Fatal("Init accepted a directory as the workspace marker")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(target, []byte("version = 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "parchment.toml")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if err := Init(root); err == nil {
			t.Fatal("Init accepted a symlink as the workspace marker")
		}
	})
}

func TestOpenRejectsUninitializedDirectoryWithoutCreatingStorage(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root); err == nil {
		t.Fatal("opened an uninitialized directory")
	}
	if _, err := os.Lstat(filepath.Join(root, ".parchment")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open created workspace storage: %v", err)
	}
}

func TestTransitionPreventsConcurrentLostUpdates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first := openTestWorkspace(t, root)
	second, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := note.NewService(first, 10).Create(ctx, "Initial", "Initial")
	if err != nil {
		t.Fatal(err)
	}
	targets := []note.Note{created, created}
	targets[0].Title, targets[0].Body = "First update", "First update"
	targets[1].Title, targets[1].Body = "Second update", "Second update"

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- first.Transition(ctx, created.ID, &created, &targets[0])
	}()
	go func() {
		<-start
		errs <- second.Transition(ctx, created.ID, &created, &targets[1])
	}()
	close(start)
	firstErr, secondErr := <-errs, <-errs
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("transition results = %v, %v; want exactly one success", firstErr, secondErr)
	}
}

func TestTransitionDoesNotOverwriteOccupiedArtifactIDs(t *testing.T) {
	ctx := context.Background()
	ws := openTestWorkspace(t, t.TempDir())
	id := "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(ws.Root(), ".parchment", "artifacts", id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := artifact.Artifact{
		ID: id, Kind: artifact.DocumentKind, Title: "Other artifact",
		CreatedAt: time.Now().UTC(), ModifiedAt: time.Now().UTC(),
		FormatVersion: artifact.FormatVersion,
		Location:      ".parchment/artifacts/" + id + "/content.md",
	}
	original, err := artifactfile.Encode(metadata, "other artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	contentPath := filepath.Join(dir, "content.md")
	if err := os.WriteFile(contentPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	target := note.Note{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.NoteKind, Title: "Overwriting note",
		CreatedAt: time.Now().UTC(), ModifiedAt: time.Now().UTC(),
		FormatVersion: artifact.FormatVersion,
		Location:      ".parchment/artifacts/" + id + "/content.md",
	}}
	if err := ws.Save(ctx, target); err == nil {
		t.Fatal("Save overwrote a non-note artifact")
	}
	if err := ws.Transition(ctx, id, nil, &target); err == nil {
		t.Fatal("transition overwrote a non-note artifact")
	}
	after, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("non-note metadata changed: %s", after)
	}

	occupiedID := "abcdef0123456789abcdef0123456789"
	occupiedDir := filepath.Join(ws.Root(), ".parchment", "artifacts", occupiedID)
	if err := os.Mkdir(occupiedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target.ID = occupiedID
	target.Location = ".parchment/artifacts/" + occupiedID + "/content.md"
	if err := ws.Transition(ctx, occupiedID, nil, &target); err == nil {
		t.Fatal("transition claimed an occupied artifact directory")
	}
	if _, err := os.Lstat(filepath.Join(occupiedDir, "content.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transition wrote into occupied directory: %v", err)
	}
}

func TestValidateMarkerRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(target, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "parchment.toml")
	if err := os.Symlink(target, marker); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := ValidateMarker(root); err == nil {
		t.Fatal("ValidateMarker accepted a symlink")
	}
}

func TestWorkspaceReportsMissingNoteContent(t *testing.T) {
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(context.Background(), "Incomplete note", "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(created.Location))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(created.Location)), []byte("# invalid"), 0o600); err != nil {
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
	ws := openTestWorkspace(t, root)
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

	contentPath := filepath.Join(root, filepath.FromSlash(created.Location))
	if err := os.Remove(contentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	invalid = created
	invalid.Body = "second replacement"
	if err := ws.Save(ctx, invalid); err == nil {
		t.Fatal("save succeeded when artifact destination was a directory")
	}
	if info, err := os.Stat(contentPath); err != nil || !info.IsDir() {
		t.Fatalf("failed save changed artifact destination: %v, %v", info, err)
	}
}

func TestFailedCreateDoesNotLeaveArtifactDirectory(t *testing.T) {
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
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

func TestOpenRejectsSymlinkedWorkspaceStorage(t *testing.T) {
	root := t.TempDir()
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".parchment", "artifacts")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".parchment", "artifacts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("opened workspace with symlinked artifact storage")
	}
}

func TestSaveRejectsSymlinkedArtifactDirectory(t *testing.T) {
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	id := "0123456789abcdef0123456789abcdef"
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".parchment", "artifacts", id)); err != nil {
		t.Fatal(err)
	}
	n := note.Note{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.NoteKind, Title: "Symlink",
		CreatedAt: time.Now().UTC(), ModifiedAt: time.Now().UTC(), FormatVersion: artifact.FormatVersion,
		Location: ".parchment/artifacts/" + id + "/content.md",
	}}
	if err := ws.Save(context.Background(), n); err == nil {
		t.Fatal("saved note through symlinked artifact directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "content.md")); !os.IsNotExist(err) {
		t.Fatalf("save wrote outside workspace: %v", err)
	}
}

func TestConcurrentSavesKeepArtifactFilesConsistent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	firstWorkspace := openTestWorkspace(t, root)
	secondWorkspace, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := note.NewService(firstWorkspace, 10).Create(ctx, "Initial", "Initial")
	if err != nil {
		t.Fatal(err)
	}

	var saves sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		saves.Add(1)
		go func(i int) {
			defer saves.Done()
			updated := created
			updated.Title = fmt.Sprintf("Note %d", i)
			updated.Body = fmt.Sprintf("Note %d", i)
			if i%2 == 0 {
				errs <- firstWorkspace.Save(ctx, updated)
			} else {
				errs <- secondWorkspace.Save(ctx, updated)
			}
		}(i)
	}
	saves.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := firstWorkspace.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != loaded.Body || !strings.HasPrefix(loaded.Title, "Note ") {
		t.Fatalf("concurrent save mixed artifact files: title=%q body=%q", loaded.Title, loaded.Body)
	}
}

func TestArtifactLockWaitsAndHonorsCancellation(t *testing.T) {
	artifactsDir := filepath.Join(t.TempDir(), ".parchment", "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "0123456789abcdef0123456789abcdef"
	unlock, err := lockArtifact(context.Background(), artifactsDir, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := lockArtifact(ctx, artifactsDir, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v, want context deadline exceeded", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	unlock, err = lockArtifact(context.Background(), artifactsDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecoversInterruptedArtifactReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Original", "original body")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	content, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	contentBackup, err := stageFile(dir, content)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := json.Marshal([]transactionFile{
		{Name: "content.md", Backup: filepath.Base(contentBackup), HadOld: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.md"), []byte("partial replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, transactionName), transaction, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != created.Title || loaded.Body != created.Body {
		t.Fatalf("recovered note = %+v, want original note", loaded)
	}
}

func TestRecoveryRejectsUnexpectedBackupBeforeMutatingFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Original", "original body")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".parchment", "artifacts", created.ID)
	backup, err := stageFile(dir, []byte("original body"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.md"), []byte("partial content"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("must remain"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal([]transactionFile{
		{Name: "content.md", Backup: filepath.Base(backup), HadOld: true},
		{Name: "unexpected.md", Backup: "../../../../victim", HadOld: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, transactionName), journal, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recoverArtifactFiles(dir); err == nil {
		t.Fatal("recovery accepted a backup for an entry without an old file")
	}
	content, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "partial content" {
		t.Fatalf("recovery mutated files before validating journal: %q", content)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "must remain" {
		t.Fatalf("unexpected external file change: content=%q err=%v", data, err)
	}
}

func TestOpenRestoresAmbiguousLegacyArtifactDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Deleted", "body")
	if err != nil {
		t.Fatal(err)
	}
	artifactsDir := filepath.Join(root, ".parchment", "artifacts")
	dir := filepath.Join(artifactsDir, created.ID)
	tombstone := filepath.Join(artifactsDir, deletedArtifactPrefix+created.ID)
	if err := os.Rename(dir, tombstone); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(ctx, created.ID); err != nil {
		t.Fatalf("ambiguous legacy deletion did not restore note: %v", err)
	}
	if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deletion tombstone remains: %v", err)
	}
}

func TestOpenRestoresPendingArtifactDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(ctx, "Restored", "body")
	if err != nil {
		t.Fatal(err)
	}
	artifactsDir := filepath.Join(root, ".parchment", "artifacts")
	dir := filepath.Join(artifactsDir, created.ID)
	tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+created.ID)
	if err := os.Rename(dir, tombstone); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Open did not restore pending note deletion: %v", err)
	}
	if loaded.Title != created.Title || loaded.Body != created.Body {
		t.Fatalf("restored note = %+v, want %+v", loaded, created)
	}
	if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending tombstone remains: %v", err)
	}
}

func TestOpenRecoversDeletionFromDurableIntent(t *testing.T) {
	for _, state := range []string{"pending", "committed"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			ws := openTestWorkspace(t, root)
			created, err := note.NewService(ws, 10).Create(ctx, "Recovery", "body")
			if err != nil {
				t.Fatal(err)
			}
			artifactsDir := filepath.Join(root, ".parchment", "artifacts")
			dir := filepath.Join(artifactsDir, created.ID)
			tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+created.ID)
			if err := os.Rename(dir, tombstone); err != nil {
				t.Fatal(err)
			}
			if err := writeArtifactDeletionIntent(artifactsDir, artifactDeletionIntent{ID: created.ID, State: state}); err != nil {
				t.Fatal(err)
			}

			reopened, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = reopened.Get(ctx, created.ID)
			if state == "pending" && err != nil {
				t.Fatalf("pending intent did not restore note: %v", err)
			}
			if state == "committed" && !errors.Is(err, note.ErrNotFound) {
				t.Fatalf("committed intent lookup error = %v, want note not found", err)
			}
			if _, err := os.Lstat(filepath.Join(artifactsDir, deleteIntentPrefix+created.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deletion intent remains: %v", err)
			}
			if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deletion tombstone remains: %v", err)
			}
		})
	}
}

func TestOpenKeepsCommittedDeletionIntentWhenTombstoneCleanupFails(t *testing.T) {
	root := t.TempDir()
	ws := openTestWorkspace(t, root)
	created, err := note.NewService(ws, 10).Create(context.Background(), "Committed", "body")
	if err != nil {
		t.Fatal(err)
	}
	artifactsDir := filepath.Join(root, ".parchment", "artifacts")
	tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+created.ID)
	if err := os.Rename(filepath.Join(artifactsDir, created.ID), tombstone); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactDeletionIntent(artifactsDir, artifactDeletionIntent{ID: created.ID, State: "committed"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(tombstone); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tombstone, []byte("invalid tombstone"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil {
		t.Fatal("Open succeeded despite invalid committed tombstone")
	}
	intentPath := filepath.Join(artifactsDir, deleteIntentPrefix+created.ID)
	if _, err := os.Stat(intentPath); err != nil {
		t.Fatalf("failed cleanup discarded committed deletion intent: %v", err)
	}

	if err := os.Remove(tombstone); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatalf("Open did not retry committed deletion cleanup: %v", err)
	}
	if _, err := os.Stat(intentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed deletion intent remains after successful retry: %v", err)
	}
}

func TestNoteRepositoryIgnoresOtherArtifactKinds(t *testing.T) {
	ws := openTestWorkspace(t, t.TempDir())
	id := "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(ws.Root(), ".parchment", "artifacts", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := artifact.Artifact{
		ID: id, Kind: artifact.DocumentKind, Title: "Future document",
		CreatedAt:     time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ModifiedAt:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		FormatVersion: artifact.FormatVersion,
		Location:      ".parchment/artifacts/" + id + "/content.md",
	}
	data, err := artifactfile.Encode(metadata, "future document", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	notes, err := ws.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("note list included non-note artifacts: %+v", notes)
	}
	if err := ws.Delete(context.Background(), id); err == nil {
		t.Fatal("Delete removed a non-note artifact")
	}
	if _, err := os.Stat(filepath.Join(dir, "content.md")); err != nil {
		t.Fatalf("Delete changed non-note artifact file: %v", err)
	}
}
