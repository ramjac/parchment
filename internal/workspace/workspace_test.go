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
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/search"
)

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
	ws := openTestWorkspace(t, t.TempDir())
	if _, err := ws.Get(context.Background(), "../outside"); err != note.ErrNotFound {
		t.Fatalf("unsafe ID error = %v, want ErrNotFound", err)
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
		FormatVersion: artifact.FormatVersion, Location: "documents/" + id,
	}
	original, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(dir, metadataName)
	if err := os.WriteFile(metadataPath, original, 0o600); err != nil {
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
	after, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("non-note metadata changed: %s", after)
	}

	missingMetadataID := "abcdef0123456789abcdef0123456789"
	missingMetadataDir := filepath.Join(ws.Root(), ".parchment", "artifacts", missingMetadataID)
	if err := os.Mkdir(missingMetadataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target.ID = missingMetadataID
	target.Location = ".parchment/artifacts/" + missingMetadataID + "/content.md"
	if err := ws.Transition(ctx, missingMetadataID, nil, &target); err == nil {
		t.Fatal("transition claimed artifact directory with missing metadata")
	}
	if _, err := os.Lstat(filepath.Join(missingMetadataDir, metadataName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transition wrote into occupied directory: %v", err)
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
	metadata, err := os.ReadFile(filepath.Join(dir, metadataName))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	metadataBackup, err := stageFile(dir, metadata)
	if err != nil {
		t.Fatal(err)
	}
	contentBackup, err := stageFile(dir, content)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := json.Marshal([]transactionFile{
		{Name: "content.md", Backup: filepath.Base(contentBackup), HadOld: true},
		{Name: metadataName, Backup: filepath.Base(metadataBackup), HadOld: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.md"), []byte("partial replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, metadataName), []byte("{}"), 0o600); err != nil {
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
		{Name: metadataName, Backup: "../../../../victim", HadOld: false},
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

func TestOpenFinishesInterruptedArtifactDeletion(t *testing.T) {
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
	if _, err := reopened.Get(ctx, created.ID); !errors.Is(err, note.ErrNotFound) {
		t.Fatalf("deleted note lookup error = %v, want note not found", err)
	}
	if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deletion tombstone remains: %v", err)
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
	if err := ws.Delete(context.Background(), id); err == nil {
		t.Fatal("Delete removed a non-note artifact")
	}
	if _, err := os.Stat(filepath.Join(dir, metadataName)); err != nil {
		t.Fatalf("Delete changed non-note artifact metadata: %v", err)
	}
}
