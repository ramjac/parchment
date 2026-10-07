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
	c.must("note", "edit", path, "--body", "updated content")
	shown := c.must("note", "show", "ideas.md")
	if shown != "updated content" {
		t.Fatalf("show output = %q", shown)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") ||
		strings.Contains(string(data), "location") || strings.Contains(string(data), `"title"`) {
		t.Fatalf("note file = %s", data)
	}
	if _, err := c.run("note", "create", "ideas.md"); err == nil {
		t.Fatal("create overwrote an existing file")
	}
	for _, removed := range [][]string{
		{"note", "list"}, {"search", "x"}, {"note", "delete", "ideas.md", "--yes"},
		{"note", "rename", "ideas.md", "x"}, {"note", "add", "ideas.md", "tag"}, {"note", "create", "x.md", "--title", "x"},
	} {
		if _, err := c.run(removed...); err == nil {
			t.Fatalf("removed command %v still runs", removed)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("note file missing: %v", err)
	}
}

func TestNoteEditRequiresBody(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "note.md", "--body", "Original body")
	if _, err := c.run("note", "edit", "note.md"); err == nil {
		t.Fatal("edit without --body succeeded")
	}
	if shown := c.must("note", "show", "note.md"); shown != "Original body" {
		t.Fatalf("show = %q", shown)
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

func TestCLIRejectsWrongKindAndKeepsPlainMarkdownPlain(t *testing.T) {
	c := newCLI(t)
	c.must("document", "create", "report.md")
	if _, err := c.run("note", "show", "report.md"); err == nil || !strings.Contains(err.Error(), "document") {
		t.Fatalf("note show on a document = %v", err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, "plain.md"), []byte("# Just Markdown\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if shown := c.must("note", "show", "plain.md"); shown != "# Just Markdown\n" {
		t.Fatalf("plain Markdown show = %q", shown)
	}
	c.must("note", "edit", "plain.md", "--body", "# Edited\n")
	if data, _ := os.ReadFile(filepath.Join(c.dir, "plain.md")); string(data) != "# Edited\n" {
		t.Fatalf("plain Markdown file after edit: %q", data)
	}
	if _, err := c.run("document", "show", "plain.md"); err == nil {
		t.Fatal("document show accepted plain Markdown")
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
	if _, kind, err := resolveTUITarget("sheet.md", ""); err != nil || kind != "spreadsheet" {
		t.Fatalf("spreadsheet in TUI = %q, %v", kind, err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, "plain.md"), []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, kind, err := resolveTUITarget("plain.md", ""); err != nil || kind != "note" {
		t.Fatalf("plain Markdown in TUI = %q, %v", kind, err)
	}
	if _, kind, err := resolveTUITarget("new.md", ""); err != nil || kind != "" {
		t.Fatalf("missing file without --kind = %q, %v", kind, err)
	}
	if _, kind, err := resolveTUITarget("new.md", "document"); err != nil || kind != "document" {
		t.Fatalf("missing file with --kind = %q, %v", kind, err)
	}
	if _, kind, err := resolveTUITarget("new.md", "presentation"); err != nil || kind != "presentation" {
		t.Fatalf("missing file with --kind presentation = %q, %v", kind, err)
	}
	if _, _, err := resolveTUITarget("new.md", "folder"); err == nil {
		t.Fatal("--kind folder accepted")
	}
	if _, err := os.Stat(filepath.Join(c.dir, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("resolving a target created the file: %v", err)
	}
}

func TestRootHelpAndSubcommandRouting(t *testing.T) {
	c := newCLI(t)
	if out := c.must(); !strings.Contains(out, "parchment [file]") {
		t.Fatalf("root help = %q", out)
	}
	if _, err := c.run("a.md", "b.md"); err == nil {
		t.Fatal("two file arguments accepted")
	}
	if out := c.must("note", "create", "note.md"); !strings.HasSuffix(strings.TrimSpace(out), "note.md") {
		t.Fatalf("subcommand routed to the editor: %q", out)
	}
}
