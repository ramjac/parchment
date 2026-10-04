package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
)

func TestStandaloneNoteEditsWithoutWorkspace(t *testing.T) {
	sample, err := os.ReadFile("../../examples/note.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenNoteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(repository, 10)
	listed, err := repository.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("list standalone note = %v, %v", listed, err)
	}
	id := listed[0].ID
	before, err := service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	after, err := service.UpdateExpected(context.Background(), before, before.Title, "# Updated\n")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Body != after.Body || repository.ArtifactLocation(before.ID) != before.Location {
		t.Fatalf("note envelope changed unexpectedly: %+v", file)
	}
	if _, err := service.Undo(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = artifactfile.Decode(data)
	if err != nil || file.Body != before.Body {
		t.Fatalf("undo file = %+v, %v", file, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v", info.Mode().Perm())
	}
}

func TestStandaloneNoteRejectsExternalChanges(t *testing.T) {
	sample, err := os.ReadFile("../../examples/note.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenNoteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repository.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("list standalone note = %v, %v", listed, err)
	}
	if err := os.WriteFile(path, append(sample, []byte("\nExternal edit\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	service := note.NewService(repository, 10)
	body := "# New body"
	if _, err := service.UpdateExpected(context.Background(), listed[0], listed[0].Title, body); err == nil ||
		!strings.Contains(err.Error(), "changed outside Parchment") {
		t.Fatalf("external edit error = %v", err)
	}
}
