package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceAndNoteCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "100")
	root := t.TempDir()

	run := func(args ...string) (string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := New(&stdout, &stderr)
		cmd.SetArgs(append([]string{"--workspace", root}, args...))
		err := cmd.Execute()
		return stdout.String(), err
	}
	if output, err := run("init"); err != nil {
		t.Fatalf("init: %v (%s)", err, output)
	}
	output, err := run("note", "create", "CLI test", "--body", "searchable content")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(output)
	if len(id) != 32 {
		t.Fatalf("created ID = %q", id)
	}
	if _, err := run("note", "add", id, "terminal"); err != nil {
		t.Fatal(err)
	}
	searchResults, err := run("search", "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchResults, id) {
		t.Fatalf("search output = %q", searchResults)
	}
	if _, err := run("note", "edit", id, "--title", "Renamed", "--body", "updated content"); err != nil {
		t.Fatal(err)
	}
	shown, err := run("note", "show", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "# Renamed") || !strings.Contains(shown, "updated content") {
		t.Fatalf("show output = %q", shown)
	}
	if _, err := run("note", "delete", id); err == nil {
		t.Fatal("delete succeeded without explicit confirmation")
	}
	if _, err := run("note", "delete", id, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root + "/.parchment/artifacts/" + id); !os.IsNotExist(err) {
		t.Fatalf("deleted artifact storage exists: %v", err)
	}
}
