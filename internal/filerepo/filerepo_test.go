package filerepo_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
)

func TestNew(t *testing.T) {
	t.Run("empty state directory returns error", func(t *testing.T) {
		repo, err := filerepo.New("")
		if err == nil {
			t.Fatal("expected error for empty stateDir, got nil")
		}
		if repo != nil {
			t.Fatalf("expected nil repository, got %v", repo)
		}
		if !strings.Contains(err.Error(), "state directory is required") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("valid directory resolves to absolute path", func(t *testing.T) {
		tempDir := t.TempDir()
		repo, err := filerepo.New(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}

		// Verify repo functionality with the initialized state directory
		ctx := context.Background()
		_, ok, err := repo.LoadRecovery(ctx, filepath.Join(tempDir, "dummy.md"))
		if err != nil || ok {
			t.Fatalf("unexpected recovery check result: ok=%v, err=%v", ok, err)
		}
	})

	t.Run("relative path converted to absolute path", func(t *testing.T) {
		t.Chdir(t.TempDir())
		relPath := filepath.Join(".", "test_state_dir")
		absPath, err := filepath.Abs(relPath)
		if err != nil {
			t.Fatal(err)
		}

		repo, err := filerepo.New(relPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}

		// Verify state directory is converted to absolute path by using LoadRecovery
		// which attempts to access stateDir/recovery
		ctx := context.Background()
		_, ok, err := repo.LoadRecovery(ctx, filepath.Join(absPath, "dummy.md"))
		if err != nil || ok {
			t.Fatalf("unexpected recovery check result: ok=%v, err=%v", ok, err)
		}
	})

	t.Run("non-existent state directory path is accepted and resolved", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistentPath := filepath.Join(tempDir, "sub", "dir")
		repo, err := filerepo.New(nonExistentPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}

		// Verify state directory is created on demand when an operation requires it
		ctx := context.Background()
		draft := recovery.Draft{
			Path:      filepath.Join(tempDir, "note.md"),
			Kind:      "note",
			Data:      json.RawMessage(`{"text":"hello"}`),
			UpdatedAt: time.Now().UTC(),
		}
		if err := repo.SaveRecovery(ctx, draft); err != nil {
			t.Fatalf("failed to save recovery in newly created state directory: %v", err)
		}

		loaded, ok, err := repo.LoadRecovery(ctx, filepath.Join(tempDir, "note.md"))
		if err != nil || !ok || string(loaded.Data) != `{"text":"hello"}` {
			t.Fatalf("failed to load recovery from state directory: loaded=%+v, ok=%v, err=%v", loaded, ok, err)
		}
	})
}

func newRepo(t *testing.T) (*filerepo.Repository, string, string) {
	t.Helper()
	state, folder := t.TempDir(), t.TempDir()
	repo, err := filerepo.New(state)
	if err != nil {
		t.Fatal(err)
	}
	return repo, state, folder
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

func TestArtifactsLiveAmongOtherFilesWithoutSidecars(t *testing.T) {
	ctx := context.Background()
	repo, state, folder := newRepo(t)
	if err := os.WriteFile(filepath.Join(folder, "shopping.txt"), []byte("milk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "ideas.md")
	notes := note.NewService(repo, 10)
	if _, err := notes.Create(ctx, path, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Update(ctx, path, "second"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, folder), ","); got != "ideas.md,shopping.txt" {
		t.Fatalf("folder contents = %s", got)
	}
	if got := strings.Join(names(t, state), ","); got != "write.lock" {
		t.Fatalf("state directory contents = %s", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") || !strings.Contains(string(data), "second") ||
		strings.Contains(string(data), `"location"`) || strings.Contains(string(data), folder) {
		t.Fatalf("file content = %s", data)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("new file mode = %v, %v", info.Mode(), err)
		}
	}
}

func TestCreateIsExclusive(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "existing.md")
	if err := os.WriteFile(path, []byte("my own file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := note.NewService(repo, 10).Create(ctx, path, ""); !errors.Is(err, filerepo.ErrExists) {
		t.Fatalf("create over existing file = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "my own file\n" {
		t.Fatalf("existing file changed: %q", data)
	}
	if _, err := note.NewService(repo, 10).Create(ctx, filepath.Join(folder, "missing", "x.md"), ""); err == nil {
		t.Fatal("create made a missing parent directory")
	}
}

func TestKindError(t *testing.T) {
	tests := []struct {
		name string
		err  filerepo.KindError
		want string
	}{
		{
			name: "document expected note",
			err: filerepo.KindError{
				Path: "doc.md",
				Kind: artifact.DocumentKind,
				Want: artifact.NoteKind,
			},
			want: "doc.md is a document, not a note",
		},
		{
			name: "spreadsheet expected presentation",
			err: filerepo.KindError{
				Path: "/tmp/sheet.md",
				Kind: artifact.SpreadsheetKind,
				Want: artifact.PresentationKind,
			},
			want: "/tmp/sheet.md is a spreadsheet, not a presentation",
		},
		{
			name: "empty kinds and path",
			err: filerepo.KindError{
				Path: "",
				Kind: "",
				Want: "",
			},
			want: " is a , not a ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("KindError.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKindDetectionAndMismatch(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "report.md")
	if _, err := document.NewService(repo, 10).Create(ctx, path, document.Draft{}); err != nil {
		t.Fatal(err)
	}
	if kind, err := filerepo.Kind(path); err != nil || kind != artifact.DocumentKind {
		t.Fatalf("Kind = %q, %v", kind, err)
	}
	_, err := repo.Get(ctx, path)
	var kindErr *filerepo.KindError
	if !errors.As(err, &kindErr) || kindErr.Kind != artifact.DocumentKind || kindErr.Want != artifact.NoteKind {
		t.Fatalf("note Get on a document = %v", err)
	}
	plain := filepath.Join(folder, "plain.md")
	if err := os.WriteFile(plain, []byte("# Plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := filerepo.Kind(plain); !errors.Is(err, filerepo.ErrNotArtifact) {
		t.Fatalf("Kind of plain Markdown = %v", err)
	}
	if _, err := repo.GetDocument(ctx, plain); !errors.Is(err, filerepo.ErrNotArtifact) {
		t.Fatalf("GetDocument of plain Markdown = %v", err)
	}
	if _, err := repo.Get(ctx, filepath.Join(folder, "absent.md")); !errors.Is(err, note.ErrNotFound) {
		t.Fatalf("Get of missing file = %v", err)
	}
}

func TestTransitionDetectsExternalEdits(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "note.md")
	notes := note.NewService(repo, 10)
	loaded, err := notes.Create(ctx, path, "original")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "original", "edited in a text editor", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.UpdateExpected(ctx, loaded, "stale"); err == nil ||
		!strings.Contains(err.Error(), "changed since it was loaded") {
		t.Fatalf("stale update = %v", err)
	}
	if current, _ := repo.Get(ctx, path); current.Body != "edited in a text editor" {
		t.Fatalf("external edit overwritten: %q", current.Body)
	}
}

func TestUpdatesPreserveModeAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	target := filepath.Join(folder, "real.md")
	notes := note.NewService(repo, 10)
	if _, err := notes.Create(ctx, target, "one"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Update(ctx, link, "two"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", info.Mode(), err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("target mode = %v, %v", info.Mode(), err)
	}
	if n, err := repo.Get(ctx, target); err != nil || n.Body != "two" {
		t.Fatalf("target = %+v, %v", n, err)
	}
}

func TestRepositoryNeverDeletesFiles(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "note.md")
	n, err := note.NewService(repo, 10).Create(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Transition(ctx, path, &n, nil); err == nil {
		t.Fatal("transition to nil succeeded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file removed: %v", err)
	}
}

func TestDocumentProposalsAreStoredInTheFile(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "doc.md")
	docs := document.NewService(repo, 10)
	created, err := docs.Create(ctx, path, document.Draft{Body: "before"})
	if err != nil {
		t.Fatal(err)
	}
	change, err := docs.Propose(ctx, created, "Edit", document.Draft{Body: "after", Layout: created.Layout})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), change.ID) {
		t.Fatalf("file lacks proposal: %s, %v", data, err)
	}
	reopened, err := filerepo.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := document.NewService(reopened, 10).Accept(ctx, path, change.ID)
	if err != nil || accepted.Body != "after" {
		t.Fatalf("accept from another process = %+v, %v", accepted, err)
	}
}

func TestSpreadsheetAndPresentationRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	sheetPath := filepath.Join(folder, "budget.md")
	book, err := spreadsheet.NewService(repo, 10).Create(ctx, sheetPath, [][]spreadsheet.Cell{{{Value: "A"}, {Value: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetSpreadsheet(ctx, sheetPath)
	if err != nil || loaded.Title != "budget" || loaded.Path != book.Path {
		t.Fatalf("spreadsheet = %+v, %v", loaded, err)
	}
	deckPath := filepath.Join(folder, "talk.md")
	if _, err := presentation.NewService(repo, 10).Create(ctx, deckPath, "# Talk\n\n## One\n\nHi\n"); err != nil {
		t.Fatal(err)
	}
	deck, err := repo.GetPresentation(ctx, deckPath)
	if err != nil || deck.Title != "talk" {
		t.Fatalf("presentation = %+v, %v", deck, err)
	}
}

func TestRecoveryDraftsLiveInStateDirectory(t *testing.T) {
	ctx := context.Background()
	repo, state, folder := newRepo(t)
	path := filepath.Join(folder, "note.md")
	if _, ok, err := repo.LoadRecovery(ctx, path); ok || err != nil {
		t.Fatalf("missing draft = %t, %v", ok, err)
	}
	draft := recovery.Draft{Path: path, Kind: "note", Data: json.RawMessage(`{"x":1}`), UpdatedAt: time.Unix(10, 0).UTC()}
	if err := repo.SaveRecovery(ctx, draft); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.LoadRecovery(ctx, path)
	if err != nil || !ok || string(loaded.Data) != `{"x":1}` || !loaded.UpdatedAt.Equal(draft.UpdatedAt) {
		t.Fatalf("loaded draft = %+v, %t, %v", loaded, ok, err)
	}
	if _, ok, _ := repo.LoadRecovery(ctx, filepath.Join(folder, "other.md")); ok {
		t.Fatal("draft returned for a different file")
	}
	if len(names(t, folder)) != 0 {
		t.Fatalf("recovery wrote next to the artifact: %v", names(t, folder))
	}
	if files := names(t, filepath.Join(state, "recovery")); len(files) != 1 {
		t.Fatalf("recovery files = %v", files)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(state, "recovery")); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("recovery directory mode = %v, %v", info.Mode(), err)
		}
	}
	if err := repo.DeleteRecovery(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteRecovery(ctx, path); err != nil {
		t.Fatalf("deleting a missing draft = %v", err)
	}
	if _, ok, _ := repo.LoadRecovery(ctx, path); ok {
		t.Fatal("deleted draft still loads")
	}
	if err := repo.SaveRecovery(ctx, recovery.Draft{Path: path, Kind: "note"}); err == nil {
		t.Fatal("empty draft saved")
	}
}

func TestPlainMarkdownOpensAsNoteAndStaysPlain(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "plain.md")
	if err := os.WriteFile(path, []byte("# Plain\n\nText.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)
	loaded, err := notes.Get(ctx, path)
	if err != nil || loaded.Body != "# Plain\n\nText.\n" || loaded.Title != "plain" ||
		!artifact.ValidIDForKind(loaded.ID, artifact.NoteKind) {
		t.Fatalf("plain note = %+v, %v", loaded, err)
	}
	if _, err := notes.UpdateExpected(ctx, loaded, "# Plain\n\nEdited.\n"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# Plain\n\nEdited.\n" {
		t.Fatalf("plain file gained an envelope: %q", data)
	}
	if _, err := notes.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# Plain\n\nText.\n" {
		t.Fatalf("undo = %q", data)
	}
	if err := os.WriteFile(path, []byte("changed elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Redo(ctx); err == nil {
		t.Fatal("redo overwrote an external edit to a plain file")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("plain file mode = %v, %v", info.Mode(), err)
		}
	}
}

func TestExamplesOpenWithPathDerivedIdentity(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := newRepo(t)
	example := func(name string) string {
		path, err := filepath.Abs(filepath.Join("../../examples", name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	n, err := repo.Get(ctx, example("note.md"))
	if err != nil || n.Title != "note" || !artifact.ValidIDForKind(n.ID, artifact.NoteKind) {
		t.Fatalf("note example = %+v, %v", n.Artifact, err)
	}
	d, err := repo.GetDocument(ctx, example("document.md"))
	if err != nil || d.Title != "document" || !artifact.ValidIDForKind(d.ID, artifact.DocumentKind) {
		t.Fatalf("document example = %+v, %v", d.Artifact, err)
	}
	b, err := repo.GetSpreadsheet(ctx, example("budget.md"))
	if err != nil || b.Title != "budget" || !artifact.ValidIDForKind(b.ID, artifact.SpreadsheetKind) {
		t.Fatalf("spreadsheet example = %+v, %v", b.Artifact, err)
	}
	p, err := repo.GetPresentation(ctx, example("presentation.md"))
	if err != nil || p.Title != "presentation" || !artifact.ValidIDForKind(p.ID, artifact.PresentationKind) {
		t.Fatalf("presentation example = %+v, %v", p.Artifact, err)
	}
}

func TestStateDirectoriesAreMadeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	state := filepath.Join(t.TempDir(), "state")
	for _, dir := range []string{state, filepath.Join(state, "recovery")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := filerepo.New(state)
	if err != nil {
		t.Fatal(err)
	}
	draft := recovery.Draft{Path: filepath.Join(t.TempDir(), "n.md"), Kind: "note", Data: json.RawMessage(`{}`)}
	if err := repo.SaveRecovery(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{state, filepath.Join(state, "recovery")} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v, %v", dir, info.Mode().Perm(), err)
		}
	}
}

func TestCancelledRecoverySaveWritesNothing(t *testing.T) {
	repo, state, folder := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	draft := recovery.Draft{Path: filepath.Join(folder, "n.md"), Kind: "note", Data: json.RawMessage(`{}`)}
	if err := repo.SaveRecovery(ctx, draft); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save = %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "recovery")); !os.IsNotExist(err) {
		t.Fatalf("cancelled save touched recovery storage: %v", err)
	}
}

func TestTransitionSpreadsheet(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	sheetPath := filepath.Join(folder, "sheet.md")

	t.Run("nil target", func(t *testing.T) {
		err := repo.TransitionSpreadsheet(ctx, sheetPath, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "Parchment does not delete artifact files") {
			t.Fatalf("expected delete error, got: %v", err)
		}
	})

	id, err := artifact.NewID(artifact.SpreadsheetKind)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	absPath, err := filepath.Abs(sheetPath)
	if err != nil {
		t.Fatal(err)
	}

	target := spreadsheet.Spreadsheet{
		Artifact: artifact.Artifact{
			ID: id, Kind: artifact.SpreadsheetKind,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Path: absPath,
		},
		Version: spreadsheet.FileVersion,
		Sheets:  []spreadsheet.Sheet{{Name: "Sheet1", Rows: [][]spreadsheet.Cell{{{Value: "100"}}}}},
	}

	t.Run("invalid target artifact", func(t *testing.T) {
		invalidTarget := target
		invalidTarget.Kind = artifact.NoteKind
		err := repo.TransitionSpreadsheet(ctx, sheetPath, nil, &invalidTarget)
		if err == nil {
			t.Fatal("expected error for target kind mismatch")
		}
	})

	t.Run("create success", func(t *testing.T) {
		if err := repo.TransitionSpreadsheet(ctx, sheetPath, nil, &target); err != nil {
			t.Fatalf("failed to create spreadsheet: %v", err)
		}
		got, err := repo.GetSpreadsheet(ctx, sheetPath)
		if err != nil {
			t.Fatalf("failed to read created spreadsheet: %v", err)
		}
		if got.Sheets[0].Rows[0][0].Value != "100" {
			t.Fatalf("unexpected cell value: %v", got.Sheets[0].Rows[0][0].Value)
		}
	})

	t.Run("create already exists", func(t *testing.T) {
		err := repo.TransitionSpreadsheet(ctx, sheetPath, nil, &target)
		if !errors.Is(err, filerepo.ErrExists) {
			t.Fatalf("expected ErrExists when creating over existing file, got: %v", err)
		}
	})

	loaded, err := repo.GetSpreadsheet(ctx, sheetPath)
	if err != nil {
		t.Fatal(err)
	}

	updated := loaded
	updated.Sheets = []spreadsheet.Sheet{{Name: "Sheet1", Rows: [][]spreadsheet.Cell{{{Value: "200"}}}}}
	updated.ModifiedAt = time.Now().UTC()

	t.Run("update success", func(t *testing.T) {
		if err := repo.TransitionSpreadsheet(ctx, sheetPath, &loaded, &updated); err != nil {
			t.Fatalf("failed to update spreadsheet: %v", err)
		}
		got, err := repo.GetSpreadsheet(ctx, sheetPath)
		if err != nil {
			t.Fatalf("failed to get updated spreadsheet: %v", err)
		}
		if got.Sheets[0].Rows[0][0].Value != "200" {
			t.Fatalf("cell value not updated: %v", got.Sheets[0].Rows[0][0].Value)
		}
	})

	t.Run("update missing file", func(t *testing.T) {
		missingPath := filepath.Join(folder, "missing.md")
		err := repo.TransitionSpreadsheet(ctx, missingPath, &loaded, &updated)
		if !errors.Is(err, spreadsheet.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("update kind mismatch", func(t *testing.T) {
		notePath := filepath.Join(folder, "doc.md")
		if _, err := document.NewService(repo, 10).Create(ctx, notePath, document.Draft{Body: "doc"}); err != nil {
			t.Fatal(err)
		}
		err := repo.TransitionSpreadsheet(ctx, notePath, &loaded, &updated)
		var kindErr *filerepo.KindError
		if !errors.As(err, &kindErr) {
			t.Fatalf("expected KindError when transitioning non-spreadsheet file, got: %v", err)
		}
	})

	t.Run("update state changed external edit", func(t *testing.T) {
		current, err := repo.GetSpreadsheet(ctx, sheetPath)
		if err != nil {
			t.Fatal(err)
		}
		// Write direct edit to file
		data, err := os.ReadFile(sheetPath)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(data), "200", "300", 1)
		if err := os.WriteFile(sheetPath, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
		err = repo.TransitionSpreadsheet(ctx, sheetPath, &current, &updated)
		if err == nil || !strings.Contains(err.Error(), "changed since it was loaded") {
			t.Fatalf("expected changed since loaded error, got: %v", err)
		}
	})

	t.Run("update target ID mismatch", func(t *testing.T) {
		current, err := repo.GetSpreadsheet(ctx, sheetPath)
		if err != nil {
			t.Fatal(err)
		}
		otherID, err := artifact.NewID(artifact.SpreadsheetKind)
		if err != nil {
			t.Fatal(err)
		}
		mismatchedTarget := current
		mismatchedTarget.ID = otherID
		err = repo.TransitionSpreadsheet(ctx, sheetPath, &current, &mismatchedTarget)
		if err == nil || !strings.Contains(err.Error(), "changed since it was loaded") {
			t.Fatalf("expected changed since loaded error for ID mismatch, got: %v", err)
		}
	})

	t.Run("update corrupted file content", func(t *testing.T) {
		corruptedPath := filepath.Join(folder, "corrupted.md")
		if err := repo.TransitionSpreadsheet(ctx, corruptedPath, nil, &target); err != nil {
			t.Fatal(err)
		}
		// Overwrite file content with invalid JSON payload in spreadsheet block
		corruptedContent := "```parchment-meta\n{\"id\":\"" + target.ID + "\",\"kind\":\"spreadsheet\",\"created_at\":\"2025-01-01T00:00:00Z\",\"modified_at\":\"2025-01-01T00:00:00Z\",\"format_version\":1}\n```\n\n```parchment-spreadsheet\n{invalid json}\n```\n"
		if err := os.WriteFile(corruptedPath, []byte(corruptedContent), 0o600); err != nil {
			t.Fatal(err)
		}
		err = repo.TransitionSpreadsheet(ctx, corruptedPath, &target, &updated)
		if err == nil {
			t.Fatal("expected error decoding corrupted spreadsheet")
		}
	})
}
