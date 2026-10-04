package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
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
	if _, err := os.Stat(root + "/parchment/artifacts/" + id); !os.IsNotExist(err) {
		t.Fatalf("deleted artifact storage exists: %v", err)
	}
}

func TestCLIUsesConfiguredArtifactDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	t.Setenv("PARCHMENT_ARTIFACT_DIR", "external/readable")
	root := t.TempDir()
	run := func(args ...string) (string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := New(&stdout, &stderr)
		cmd.SetArgs(append([]string{"--workspace", root}, args...))
		err := cmd.Execute()
		return stdout.String(), err
	}
	if _, err := run("init"); err != nil {
		t.Fatal(err)
	}
	created, err := run("note", "create", "Visible artifact")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(created)
	if _, err := os.Stat(filepath.Join(root, "external", "readable", id, "content.md")); err != nil {
		t.Fatalf("configured artifact file: %v", err)
	}
}

func TestCLIListAndSearchEscapeTerminalFields(t *testing.T) {
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

	if _, err := run("init"); err != nil {
		t.Fatal(err)
	}
	title := "unsafe\n\x1b[2Jtitle needle"
	created, err := run("note", "create", title)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(created)
	if _, err := run("note", "add", id, "unsafe\n\x1b]52;c;payload\a"); err != nil {
		t.Fatal(err)
	}

	listing, err := run("note", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(listing, "\x1b\a") || strings.Count(listing, "\n") != 1 {
		t.Fatalf("list output contains raw controls or forged rows: %q", listing)
	}
	if !strings.Contains(listing, strconv.Quote(title)) {
		t.Fatalf("list output did not quote title: %q", listing)
	}
	searchResults, err := run("search", "needle")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(searchResults, "\x1b\a") || strings.Count(searchResults, "\n") != 1 {
		t.Fatalf("search output contains raw controls or forged rows: %q", searchResults)
	}
	if !strings.Contains(searchResults, strconv.Quote(title)) {
		t.Fatalf("search output did not quote title: %q", searchResults)
	}
}

func TestCLIEditChangesOnlySpecifiedFields(t *testing.T) {
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

	if _, err := run("init"); err != nil {
		t.Fatal(err)
	}
	created, err := run("note", "create", "Original title", "--body", "Original body")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(created)
	if _, err := run("note", "edit", id, "--title", "New title"); err != nil {
		t.Fatal(err)
	}
	shown, err := run("note", "show", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "# New title") || !strings.Contains(shown, "Original body") {
		t.Fatalf("title-only edit changed unspecified body: %q", shown)
	}
	if _, err := run("note", "edit", id, "--body", "New body"); err != nil {
		t.Fatal(err)
	}
	shown, err = run("note", "show", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "# New title") || !strings.Contains(shown, "New body") {
		t.Fatalf("body-only edit changed unspecified title: %q", shown)
	}
}

func TestOpenRejectsSymlinkedWorkspaceConfigBeforeParsing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "100")
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(target, []byte("not valid TOML = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "parchment.toml")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := New(&stdout, &stderr)
	cmd.SetArgs([]string{"--workspace", root, "note", "list"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("CLI error = %v, want non-regular marker error before TOML parsing", err)
	}
}
