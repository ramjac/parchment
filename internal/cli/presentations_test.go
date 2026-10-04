package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	root := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "talk.md")
	source := "# A Talk\n\n## Welcome\n\nHello **everyone**.\n// private comment\n: speaker note\n\n## Wrap Up\n\nThanks.\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := New(&stdout, &stderr)
		cmd.SetArgs(append([]string{"--workspace", root}, args...))
		err := cmd.Execute()
		return stdout.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	must("init")
	id := strings.TrimSpace(must("presentation", "create", "A Talk", "--body-file", sourcePath))
	if len(id) < 2 || len(id) > 26 || id[0] != 'p' {
		t.Fatalf("presentation ID = %q", id)
	}
	if got := must("presentation", "show", id); got != source {
		t.Fatalf("show source = %q, want %q", got, source)
	}
	preview := must("presentation", "preview", id)
	for _, want := range []string{"Welcome", "Hello **everyone**.", "Wrap Up", "Thanks."} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "speaker note") || strings.Contains(preview, "private comment") {
		t.Fatalf("preview exposed notes or comments:\n%s", preview)
	}
	if out := must("presentation", "list"); !strings.Contains(out, id) || !strings.Contains(out, "2 slides") {
		t.Fatalf("presentation list = %q", out)
	}
	editedPath := filepath.Join(t.TempDir(), "edited.md")
	editedSource := "# Renamed Talk\n\n## Welcome\n\nUpdated text.\n"
	if err := os.WriteFile(editedPath, []byte(editedSource), 0o600); err != nil {
		t.Fatal(err)
	}
	must("presentation", "edit", id, "--body-file", editedPath)
	if out := must("presentation", "show", id); out != editedSource {
		t.Fatalf("edited source = %q", out)
	}
	if _, err := run("presentation", "create", "Mismatch", "--body-file", sourcePath); err == nil {
		t.Fatal("mismatched Markdown title was accepted")
	}
	must("presentation", "delete", id, "--yes")
	if out := must("presentation", "list"); strings.Contains(out, id) {
		t.Fatalf("deleted presentation still listed: %q", out)
	}
}
