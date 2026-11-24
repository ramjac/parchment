package filerepo

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/history"
	"github.com/ramjac/parchment/internal/note"
	"github.com/ramjac/parchment/internal/presentation"
	"github.com/ramjac/parchment/internal/spreadsheet"
)

func TestPersistentHistoryBaselineUndoRedoAndExternalEditGuard(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	repo, err := New(state, Options{UndoLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")
	service := note.NewService(repo, 2)
	if _, err := service.Create(ctx, path, "initial"); err != nil {
		t.Fatal(err)
	}
	if canUndo, err := repo.CanUndo(ctx, path); err != nil || canUndo {
		t.Fatalf("CanUndo() after create = %t, %v; want false, nil", canUndo, err)
	}
	if _, err := service.Update(ctx, path, "updated"); err != nil {
		t.Fatal(err)
	}
	if canUndo, err := repo.CanUndo(ctx, path); err != nil || !canUndo {
		t.Fatalf("CanUndo() after update = %t, %v; want true, nil", canUndo, err)
	}
	entries, head, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || head != 1 || entries[0].Description != "Initial state" {
		t.Fatalf("history = %d entries at head %d: %#v", len(entries), head, entries)
	}
	canonical, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.readHistoryManifest(dir, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := manifest.Entries[1].Snapshot; snapshot.RawBlobHash != "" ||
		snapshot.MetadataBlobHash == "" || snapshot.BodyBlobHash == "" {
		t.Fatalf("canonical artifact was not chunked: %#v", snapshot)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{filepath.Join(state, "history"), dir, filepath.Join(dir, "index.json")} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0o700)
			if !info.IsDir() {
				want = 0o600
			}
			if info.Mode().Perm() != want {
				t.Fatalf("%s permissions = %o, want %o", path, info.Mode().Perm(), want)
			}
		}
	}

	if _, err := repo.Undo(ctx, path); err != nil {
		t.Fatal(err)
	}
	item, err := service.Get(ctx, path)
	if err != nil || item.Body != "initial" {
		t.Fatalf("note after Undo = %q, %v; want initial", item.Body, err)
	}
	if canRedo, err := repo.CanRedo(ctx, path); err != nil || !canRedo {
		t.Fatalf("CanRedo() after Undo = %t, %v; want true, nil", canRedo, err)
	}
	if _, err := repo.Redo(ctx, path); err != nil {
		t.Fatal(err)
	}
	item, err = service.Get(ctx, path)
	if err != nil || item.Body != "updated" {
		t.Fatalf("note after Redo = %q, %v; want updated", item.Body, err)
	}

	reopened, err := New(state, Options{UndoLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	reopenedService := note.NewService(reopened, 2)
	if _, err := reopenedService.Get(ctx, path); err != nil {
		t.Fatal(err)
	}
	if !reopenedService.CanUndo() {
		t.Fatal("new repository instance lost persisted undo availability")
	}
	if _, err := reopenedService.Undo(ctx); err != nil {
		t.Fatalf("undo after reopening repository: %v", err)
	}
	item, err = reopenedService.Get(ctx, path)
	if err != nil || item.Body != "initial" {
		t.Fatalf("note after reopened Undo = %q, %v; want initial", item.Body, err)
	}
	if _, err := reopenedService.Redo(ctx); err != nil {
		t.Fatalf("redo after reopening repository: %v", err)
	}

	if err := os.WriteFile(path, []byte("external edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Undo(ctx, path); err == nil || !strings.Contains(err.Error(), "modified externally") {
		t.Fatalf("Undo() after external edit error = %v; want conflict", err)
	}
}

func TestPersistentHistoryPreparationFailureDoesNotReplaceArtifact(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	repo, err := New(state, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prepare-failure.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blobs := filepath.Join(repo.historyDir(), "blobs")
	if err := os.RemoveAll(blobs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobs, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "after"); err == nil {
		t.Fatal("update succeeded without being able to prepare its history snapshot")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("artifact changed when history preparation failed")
	}
}

func TestPersistentHistoryResetsWhenCreatingAtReusedPath(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{UndoLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reused.md")
	service := note.NewService(repo, 3)
	if _, err := service.Create(ctx, path, "old artifact"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "old edit"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, path, "new artifact"); err != nil {
		t.Fatalf("create at reused path: %v", err)
	}
	entries, head, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || head != 0 || entries[0].Description != "Initial state" {
		t.Fatalf("history after path reuse = %#v at %d; want one fresh baseline", entries, head)
	}
	if _, err := service.Update(ctx, path, "new edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(ctx, path)
	if err != nil || current.Body != "new artifact" {
		t.Fatalf("undo after path reuse = %q, %v; want new artifact", current.Body, err)
	}
}

func TestPersistentHistoryDetachesEmbeddedImageChunks(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	repo, err := New(state, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "slides.md")
	service := presentation.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "# Demo\n\n## First\n\n"); err != nil {
		t.Fatal(err)
	}
	var imageBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageBytes, img); err != nil {
		t.Fatal(err)
	}
	updated, _, err := service.AddImage(ctx, path, "red pixel", imageBytes.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, updated, updated.Source+"\n## Second\n\n"); err != nil {
		t.Fatal(err)
	}
	canonical, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.readHistoryManifest(dir, canonical)
	if err != nil {
		t.Fatal(err)
	}
	var imageHash string
	for _, entry := range manifest.Entries {
		for hash := range entry.Snapshot.ImageBlobHashes {
			if imageHash != "" && imageHash != hash {
				t.Fatalf("embedded image identity changed between snapshots: %s vs %s", imageHash, hash)
			}
			imageHash = hash
		}
	}
	if imageHash == "" {
		t.Fatal("embedded image was not detached into a content-addressed chunk")
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Images) != 1 || !bytes.Equal(restored.Images[0].Data, imageBytes.Bytes()) {
		t.Fatalf("embedded image not restored exactly: %#v", restored.Images)
	}
}

func TestPersistentHistoryCleanCollectsBlobs(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	repo, err := New(state, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "initial"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "updated"); err != nil {
		t.Fatal(err)
	}
	blobDir := filepath.Join(state, "history", "blobs")
	if blobs, err := os.ReadDir(blobDir); err != nil || len(blobs) == 0 {
		t.Fatalf("history blobs = %v, %v; want stored snapshots", blobs, err)
	}
	orphan := filepath.Join(blobDir, strings.Repeat("0", 64))
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Log(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("history log mutated orphan blobs: %v", err)
	}
	if err := repo.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("history GC retained orphan blob: %v", err)
	}
	if err := repo.Clean(ctx, path); err != nil {
		t.Fatal(err)
	}
	if blobs, err := os.ReadDir(blobDir); err != nil || len(blobs) != 0 {
		t.Fatalf("blobs after clean = %v, %v; want none", blobs, err)
	}
	if canUndo, err := repo.CanUndo(ctx, path); err != nil || canUndo {
		t.Fatalf("CanUndo() after clean = %t, %v; want false, nil", canUndo, err)
	}
}

func TestPersistentHistoryEnforcesUpdatedLimitAndTruncatesRedo(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	path := filepath.Join(t.TempDir(), "limit.md")
	repo, err := New(state, Options{UndoLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "zero"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := service.Update(ctx, path, body); err != nil {
			t.Fatal(err)
		}
	}

	reopened, err := New(state, Options{UndoLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	reopenedService := note.NewService(reopened, 1)
	if _, err := reopenedService.Get(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := reopenedService.Update(ctx, path, "four"); err != nil {
		t.Fatal(err)
	}
	entries, head, err := reopened.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || head != 1 {
		t.Fatalf("history after limit shrink = %d entries at head %d; want 2 at 1", len(entries), head)
	}
	if _, err := reopenedService.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reopenedService.Update(ctx, path, "branch"); err != nil {
		t.Fatal(err)
	}
	if canRedo, err := reopened.CanRedo(ctx, path); err != nil || canRedo {
		t.Fatalf("CanRedo() after branching = %t, %v; want false, nil", canRedo, err)
	}
}

func TestPersistentHistoryRecoversInterruptedTransition(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	repo, err := New(state, Options{})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "journal.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "after"); err != nil {
		t.Fatal(err)
	}
	canonical, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.readHistoryManifest(dir, canonical)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.reconstructHistorySnapshot(ctx, manifest.Entries[0].Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.reconstructHistorySnapshot(ctx, manifest.Entries[1].Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(history.Journal{
		TransitionID: "interrupted", FromDigest: historyDigest(after),
		ToDigest: historyDigest(before), TargetHead: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if canUndo, err := repo.CanUndo(ctx, path); err != nil || canUndo {
		t.Fatalf("CanUndo() after journal recovery = %t, %v; want false, nil", canUndo, err)
	}
	if canRedo, err := repo.CanRedo(ctx, path); err != nil || !canRedo {
		t.Fatalf("CanRedo() after journal recovery = %t, %v; want true, nil", canRedo, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "journal.json")); !os.IsNotExist(err) {
		t.Fatalf("journal remains after recovery: %v", err)
	}
}

func TestPersistentHistoryRecoversStagedManifestAfterArtifactWrite(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "staged-journal.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "after"); err != nil {
		t.Fatal(err)
	}
	canonical, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.readHistoryManifest(dir, canonical)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.reconstructHistorySnapshot(ctx, manifest.Entries[0].Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.reconstructHistorySnapshot(ctx, manifest.Entries[1].Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	manifest.HeadIndex = 0
	pending, err := encodeHistoryManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pendingHistoryManifest), pending, 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(history.Journal{
		TransitionID: "staged-interrupted",
		FromDigest:   historyDigest(after), ToDigest: historyDigest(before),
		TargetHead: 0, ManifestDigest: historyDigest(pending),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if canUndo, err := repo.CanUndo(ctx, path); err != nil || canUndo {
		t.Fatalf("CanUndo() after staged recovery = %t, %v; want false, nil", canUndo, err)
	}
	if canRedo, err := repo.CanRedo(ctx, path); err != nil || !canRedo {
		t.Fatalf("CanRedo() after staged recovery = %t, %v; want true, nil", canRedo, err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingHistoryManifest)); !os.IsNotExist(err) {
		t.Fatalf("pending manifest remains after recovery: %v", err)
	}
}

func TestPersistentHistoryExternalRestorePersistsTruncatedTargetBeforeTransition(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{UndoLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restore-target.md")
	service := note.NewService(repo, 5)
	for _, body := range []string{"zero", "one", "two"} {
		if body == "zero" {
			if _, err := service.Create(ctx, path, body); err != nil {
				t.Fatal(err)
			}
		} else if _, err := service.Update(ctx, path, body); err != nil {
			t.Fatal(err)
		}
	}
	entries, _, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	targetID := entries[2].ID
	if _, err := repo.Undo(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(dir, pendingHistoryManifest)
	if err := os.Mkdir(pendingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingPath, "blocker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Restore(ctx, path, targetID); err == nil {
		t.Fatal("restore succeeded despite the blocked transition manifest path")
	}
	if err := os.Remove(filepath.Join(pendingPath, "blocker")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pendingPath); err != nil {
		t.Fatal(err)
	}
	entries, head, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if head < 0 || head >= len(entries) || entries[head].Description != "External edit" {
		t.Fatalf("staged history head = %d in %#v; want external checkpoint", head, entries)
	}
	if canRedo, err := repo.CanRedo(ctx, path); err != nil || !canRedo {
		t.Fatalf("CanRedo() after interrupted restore = %t, %v; want true, nil", canRedo, err)
	}
	if _, err := repo.Redo(ctx, path); err != nil {
		t.Fatalf("redo persisted restore target: %v", err)
	}
	current, err := service.Get(ctx, path)
	if err != nil || current.Body != "two" {
		t.Fatalf("redo restored %q, %v; want two", current.Body, err)
	}
}

func TestPersistentHistoryCheckpointsExternalContentOnNewEdit(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "external.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("edited outside parchment"), 0o600); err != nil {
		t.Fatal(err)
	}
	external, err := service.Get(ctx, path)
	if err != nil || external.Body != "edited outside parchment" {
		t.Fatalf("external note = %q, %v", external.Body, err)
	}
	if _, err := service.UpdateExpected(ctx, external, "new save"); err != nil {
		t.Fatal(err)
	}
	entries, _, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[2].Description != "External edit" {
		t.Fatalf("history after external save = %#v; want external checkpoint and new edit", entries)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(ctx, path)
	if err != nil || current.Body != "edited outside parchment" {
		t.Fatalf("undo did not restore external checkpoint: %q, %v", current.Body, err)
	}
}

func TestPersistentHistoryRestorePreservesExternalCheckpointAndIsBounded(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{UndoLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restore-bounded.md")
	service := note.NewService(repo, 1)
	if _, err := service.Create(ctx, path, "zero"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, "two"); err != nil {
		t.Fatal(err)
	}
	entries, _, err := repo.Log(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	targetID := entries[0].ID
	if _, err := repo.Undo(ctx, path); err != nil {
		t.Fatal(err)
	}

	for _, external := range []string{"external one", "external two", "external three"} {
		if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Restore(ctx, path, targetID); err != nil {
			t.Fatalf("restore after %q: %v", external, err)
		}
		current, err := service.Get(ctx, path)
		if err != nil || current.Body != "one" {
			t.Fatalf("restored content = %q, %v; want one", current.Body, err)
		}
		entries, head, err := repo.Log(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 2 || head != 0 || len(entries) < 2 {
			t.Fatalf("bounded history = %d entries at head %d; want 2 entries at head 0", len(entries), head)
		}
		if _, err := repo.Redo(ctx, path); err != nil {
			t.Fatalf("redo to external checkpoint: %v", err)
		}
		current, err = service.Get(ctx, path)
		if err != nil || current.Body != external {
			t.Fatalf("redo restored %q, %v; want external checkpoint %q", current.Body, err, external)
		}
		if _, err := repo.Undo(ctx, path); err != nil {
			t.Fatalf("undo to restore target: %v", err)
		}
	}
}

func TestPersistentHistoryUsesSymlinkTargetIdentity(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	link := filepath.Join(filepath.Dir(target), "link.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, target, "before"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, target, "after"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if canUndo, err := repo.CanUndo(ctx, link); err != nil || !canUndo {
		t.Fatalf("CanUndo() through symlink = %t, %v; want true, nil", canUndo, err)
	}
	if _, err := repo.Undo(ctx, link); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || !bytes.Contains(data, []byte("before")) {
		t.Fatalf("symlink undo target = %q, %v; want before", data, err)
	}
}

func TestPersistentHistoryFallsBackToExactRawBytes(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "hand-edited.md")
	service := note.NewService(repo, 5)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := bytes.Replace(original, []byte(`"kind": "note"`), []byte(`"kind" : "note"`), 1)
	if bytes.Equal(original, external) {
		t.Fatal("test fixture did not alter metadata formatting")
	}
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateExpected(ctx, current, "after"); err != nil {
		t.Fatal(err)
	}
	canonical, dir, err := repo.historyPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.readHistoryManifest(dir, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 3 || manifest.Entries[1].Snapshot.RawBlobHash == "" {
		t.Fatalf("hand-edited snapshot did not use raw-byte fallback: %#v", manifest.Entries)
	}
	if _, err := repo.Undo(ctx, path); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, external) {
		t.Fatalf("raw fallback restored %q, %v; want exact bytes %q", restored, err, external)
	}
}

func TestPersistentHistoryAcrossArtifactServices(t *testing.T) {
	ctx := context.Background()
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}

	docPath := filepath.Join(t.TempDir(), "doc.md")
	docs := document.NewService(repo, 5)
	doc, err := docs.Create(ctx, docPath, document.Draft{Body: "before"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Save(ctx, doc, document.Draft{Body: "after", Layout: doc.Layout}); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if current, err := docs.Get(ctx, docPath); err != nil || current.Body != "before" {
		t.Fatalf("document after undo = %q, %v; want before", current.Body, err)
	}

	sheetPath := filepath.Join(t.TempDir(), "sheet.md")
	sheets := spreadsheet.NewService(repo, 5)
	if _, err := sheets.Create(ctx, sheetPath, [][]spreadsheet.Cell{{{Value: "before"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sheets.SetCell(ctx, sheetPath, 1, 1, spreadsheet.Cell{Value: "after"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sheets.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if current, err := sheets.Get(ctx, sheetPath); err != nil || current.Sheets[0].Rows[0][0].Value != "before" {
		t.Fatalf("spreadsheet after undo = %#v, %v; want before", current.Sheets, err)
	}

	presentationPath := filepath.Join(t.TempDir(), "presentation.md")
	presentations := presentation.NewService(repo, 5)
	pres, err := presentations.Create(ctx, presentationPath, "# Demo\n\n## First\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := presentations.Update(ctx, pres, "# Demo\n\n## Second\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := presentations.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if current, err := presentations.Get(ctx, presentationPath); err != nil || current.Source != "# Demo\n\n## First\n\n" {
		t.Fatalf("presentation after undo = %q, %v; want original source", current.Source, err)
	}
}
