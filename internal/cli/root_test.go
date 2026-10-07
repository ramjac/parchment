package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cliRunner struct {
	t    *testing.T
	home string
	dir  string
}

// newCLI isolates the CLI from the real home directory and returns a runner
// whose working directory is an ordinary temporary folder.
func newCLI(t *testing.T) cliRunner {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PARCHMENT_CONFIG", "")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "100")
	dir := t.TempDir()
	t.Chdir(dir)
	return cliRunner{t: t, home: home, dir: dir}
}

func (c cliRunner) run(args ...string) (string, error) {
	c.t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := New(&stdout, &stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

func (c cliRunner) must(args ...string) string {
	c.t.Helper()
	out, err := c.run(args...)
	if err != nil {
		c.t.Fatalf("%v: %v", args, err)
	}
	return out
}

func TestNoteCLI(t *testing.T) {
	c := newCLI(t)
	output := c.must("note", "create", "ideas.md", "--body", "first content")
	path := filepath.Join(c.dir, "ideas.md")
	if got := strings.TrimSpace(output); got != path {
		t.Fatalf("create printed %q, want %q", got, path)
	}
	c.must("note", "add", "ideas.md", "terminal")
	c.must("note", "edit", path, "--title", "Renamed", "--body", "updated content")
	shown := c.must("note", "show", "ideas.md")
	if !strings.Contains(shown, "# Renamed") || !strings.Contains(shown, "updated content") {
		t.Fatalf("show output = %q", shown)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") || !strings.Contains(string(data), `"terminal"`) ||
		strings.Contains(string(data), "location") {
		t.Fatalf("note file = %s", data)
	}
	if _, err := c.run("note", "create", "ideas.md"); err == nil {
		t.Fatal("create overwrote an existing file")
	}
	for _, removed := range [][]string{{"note", "list"}, {"search", "x"}, {"note", "delete", "ideas.md", "--yes"}} {
		if _, err := c.run(removed...); err == nil {
			t.Fatalf("removed command %v still runs", removed)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("note file missing: %v", err)
	}
}

func TestNoteCreateTitleDefaultsToFileName(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "Meeting notes.md")
	if shown := c.must("note", "show", "Meeting notes.md"); !strings.HasPrefix(shown, "# Meeting notes\n") {
		t.Fatalf("show = %q", shown)
	}
	c.must("note", "create", "other.md", "--title", "Explicit")
	if shown := c.must("note", "show", "other.md"); !strings.HasPrefix(shown, "# Explicit\n") {
		t.Fatalf("show = %q", shown)
	}
}

func TestCLIEditChangesOnlySpecifiedFields(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "note.md", "--title", "Original title", "--body", "Original body")
	c.must("note", "edit", "note.md", "--title", "New title")
	shown := c.must("note", "show", "note.md")
	if !strings.Contains(shown, "# New title") || !strings.Contains(shown, "Original body") {
		t.Fatalf("title-only edit changed unspecified body: %q", shown)
	}
	c.must("note", "edit", "note.md", "--body", "New body")
	shown = c.must("note", "show", "note.md")
	if !strings.Contains(shown, "# New title") || !strings.Contains(shown, "New body") {
		t.Fatalf("body-only edit changed unspecified title: %q", shown)
	}
}

func TestCLIKeepsOnlyConfigAndStateInHome(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "local.md")
	configPath := filepath.Join(c.home, ".parchment", "parchment.toml")
	if data, err := os.ReadFile(configPath); err != nil || string(data) != "version = 1\n" {
		t.Fatalf("default config = %q, %v", data, err)
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "local.md" {
		t.Fatalf("working directory entries = %v, %v", entries, err)
	}
	stateEntries, err := os.ReadDir(filepath.Join(c.home, ".parchment"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range stateEntries {
		if strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "artifacts" {
			t.Fatalf("state directory holds artifacts: %s", entry.Name())
		}
	}

	custom := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("PARCHMENT_CONFIG", custom)
	if _, err := c.run("note", "show", "local.md"); err == nil {
		t.Fatal("missing PARCHMENT_CONFIG file did not fail")
	}
}

func TestCLIRejectsWrongKindAndPlainMarkdown(t *testing.T) {
	c := newCLI(t)
	c.must("document", "create", "report.md")
	if _, err := c.run("note", "show", "report.md"); err == nil || !strings.Contains(err.Error(), "document") {
		t.Fatalf("note show on a document = %v", err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, "plain.md"), []byte("# Just Markdown\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run("note", "edit", "plain.md", "--body", "x"); err == nil {
		t.Fatal("edited a plain Markdown file that is not a Parchment artifact")
	}
	if data, _ := os.ReadFile(filepath.Join(c.dir, "plain.md")); string(data) != "# Just Markdown\n" {
		t.Fatalf("plain Markdown file changed: %q", data)
	}
}

func TestResolveTUITarget(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "note.md")
	c.must("spreadsheet", "create", "sheet.md")
	path, kind, err := resolveTUITarget("note.md", "")
	if err != nil || kind != "note" || path != filepath.Join(c.dir, "note.md") {
		t.Fatalf("existing note = %q, %q, %v", path, kind, err)
	}
	if _, _, err := resolveTUITarget("note.md", "document"); err == nil {
		t.Fatal("--kind document accepted for a note")
	}
	if _, _, err := resolveTUITarget("sheet.md", ""); err == nil || !strings.Contains(err.Error(), "parchment spreadsheet") {
		t.Fatalf("spreadsheet in TUI = %v", err)
	}
	if _, kind, err := resolveTUITarget("new.md", ""); err != nil || kind != "" {
		t.Fatalf("missing file without --kind = %q, %v", kind, err)
	}
	if _, kind, err := resolveTUITarget("new.md", "document"); err != nil || kind != "document" {
		t.Fatalf("missing file with --kind = %q, %v", kind, err)
	}
	if _, _, err := resolveTUITarget("new.md", "spreadsheet"); err == nil {
		t.Fatal("--kind spreadsheet accepted")
	}
	if _, err := os.Stat(filepath.Join(c.dir, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("resolving a target created the file: %v", err)
	}
}
