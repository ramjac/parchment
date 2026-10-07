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
	if _, err := notes.Create(ctx, path, "Ideas", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Update(ctx, path, "Ideas", "second"); err != nil {
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
	if _, err := note.NewService(repo, 10).Create(ctx, path, "Note", ""); !errors.Is(err, filerepo.ErrExists) {
		t.Fatalf("create over existing file = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "my own file\n" {
		t.Fatalf("existing file changed: %q", data)
	}
	if _, err := note.NewService(repo, 10).Create(ctx, filepath.Join(folder, "missing", "x.md"), "Note", ""); err == nil {
		t.Fatal("create made a missing parent directory")
	}
}

func TestKindDetectionAndMismatch(t *testing.T) {
	ctx := context.Background()
	repo, _, folder := newRepo(t)
	path := filepath.Join(folder, "report.md")
	if _, err := document.NewService(repo, 10).Create(ctx, path, document.Draft{Title: "Report"}); err != nil {
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
	if _, err := repo.Get(ctx, plain); !errors.Is(err, filerepo.ErrNotArtifact) {
		t.Fatalf("Get of plain Markdown = %v", err)
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
	loaded, err := notes.Create(ctx, path, "Title", "original")
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
	if _, err := notes.UpdateExpected(ctx, loaded, "Title", "stale"); err == nil ||
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
	if _, err := notes.Create(ctx, target, "Real", "one"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Update(ctx, link, "Real", "two"); err != nil {
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
	n, err := note.NewService(repo, 10).Create(ctx, path, "Title", "")
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
	created, err := docs.Create(ctx, path, document.Draft{Title: "Doc", Body: "before"})
	if err != nil {
		t.Fatal(err)
	}
	change, err := docs.Propose(ctx, created, "Edit", document.Draft{Title: "Doc", Body: "after", Layout: created.Layout})
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
	book, err := spreadsheet.NewService(repo, 10).Create(ctx, sheetPath, "Budget", [][]spreadsheet.Cell{{{Value: "A"}, {Value: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetSpreadsheet(ctx, sheetPath)
	if err != nil || loaded.Title != "Budget" || loaded.Path != book.Path {
		t.Fatalf("spreadsheet = %+v, %v", loaded, err)
	}
	deckPath := filepath.Join(folder, "talk.md")
	if _, err := presentation.NewService(repo, 10).Create(ctx, deckPath, "Talk", "# Talk\n\n## One\n\nHi\n"); err != nil {
		t.Fatal(err)
	}
	deck, err := repo.GetPresentation(ctx, deckPath)
	if err != nil || deck.Title != "Talk" {
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
	draft := recovery.Draft{Path: path, Kind: "note", Title: "Draft", Data: json.RawMessage(`{"x":1}`), UpdatedAt: time.Unix(10, 0).UTC()}
	if err := repo.SaveRecovery(ctx, draft); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.LoadRecovery(ctx, path)
	if err != nil || !ok || loaded.Title != "Draft" || string(loaded.Data) != `{"x":1}` || !loaded.UpdatedAt.Equal(draft.UpdatedAt) {
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
