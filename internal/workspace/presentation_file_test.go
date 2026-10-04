package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/presentation"
)

func copyPresentationExample(t *testing.T, extra string) (string, []byte) {
	t.Helper()
	sample, err := os.ReadFile("../../examples/presentation.md")
	if err != nil {
		t.Fatal(err)
	}
	sample = append(sample, []byte("\n```parchment-comments\n"+extra+"\n```\n")...)
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.md")
	if err := os.WriteFile(path, sample, 0o640); err != nil {
		t.Fatal(err)
	}
	return path, sample
}

func TestStandalonePresentationEditsWithoutWorkspace(t *testing.T) {
	path, _ := copyPresentationExample(t, `{"comments":[{"text":"keep me"}]}`)
	repository, err := OpenPresentationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := presentation.NewService(repository, 10)
	ctx := context.Background()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	id := items[0].ID
	item, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "deck" || repository.ArtifactLocation(item.ID) != item.Location {
		t.Fatalf("unexpected presentation: %+v", item.Artifact)
	}
	if _, err := service.Get(ctx, id+"x"); err != presentation.ErrNotFound {
		t.Fatalf("missing ID error = %v", err)
	}
	original := item.Source
	source := strings.Replace(original, "## What shipped", "## What shipped today", 1)
	updated, err := service.Update(ctx, item, source)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetPresentation(ctx, id)
	if err != nil || current.Title != "deck" || current.Location != item.Location {
		t.Fatalf("updated runtime metadata = %+v, %v", current.Artifact, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedFile, err := OpenPresentationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := reopenedFile.GetPresentation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Source != source || reopened.Location != item.Location ||
		reopened.CreatedAt != item.CreatedAt || !reopened.ModifiedAt.Equal(updated.ModifiedAt) {
		t.Fatalf("save lost presentation data: %+v", reopened.Artifact)
	}
	if string(reopened.Blocks["parchment-comments"]) == "" ||
		!strings.Contains(string(contents), `"keep me"`) {
		t.Fatalf("save dropped extra block:\n%s", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("file permissions = %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("standalone save created extra files: %v", entries)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedFile, err = OpenPresentationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err = reopenedFile.GetPresentation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Source != original {
		t.Fatalf("undo did not persist: %q", reopened.Source)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	if current, _ := repository.GetPresentation(ctx, id); current.Source != source {
		t.Fatalf("redo did not apply: %q", current.Source)
	}
}

func TestStandalonePresentationRejectsExternalChangesAndInvalidSource(t *testing.T) {
	path, sample := copyPresentationExample(t, `{}`)
	repository, err := OpenPresentationFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := presentation.NewService(repository, 10)
	ctx := context.Background()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %v, %v", items, err)
	}
	item, err := service.Get(ctx, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, item, "no title here"); err == nil {
		t.Fatal("invalid Markdown was accepted")
	}
	if err := os.WriteFile(path, append(sample, []byte("\nExternal edit\n")...), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(ctx, item, item.Source+"\nMore\n")
	if err == nil || !strings.Contains(err.Error(), "changed outside Parchment") {
		t.Fatalf("external edit error = %v", err)
	}
	if _, err := service.Create(ctx, "New", ""); err == nil {
		t.Fatal("standalone repository created a second presentation")
	}
}

func TestOpenPresentationFileRejectsOtherArtifacts(t *testing.T) {
	if _, err := OpenPresentationFile("../../examples/budget.md"); err == nil ||
		!strings.Contains(err.Error(), "not a presentation") {
		t.Fatalf("spreadsheet open error = %v", err)
	}
	if _, err := OpenPresentationFile(filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("missing file opened")
	}
}
