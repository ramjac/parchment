package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	sourcePath := filepath.Join(t.TempDir(), "talk.md")
	source := "# A Talk\n\n## Welcome\n\nHello **everyone**.\n// private comment\n: speaker note\n\n## Wrap Up\n\nThanks.\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "talk.md"
	if out := strings.TrimSpace(must("presentation", "create", id, "--body-file", sourcePath)); !strings.HasSuffix(out, id) {
		t.Fatalf("create printed %q", out)
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
	editedPath := filepath.Join(t.TempDir(), "edited.md")
	editedSource := "# Renamed Talk\n\n## Welcome\n\nUpdated text.\n"
	if err := os.WriteFile(editedPath, []byte(editedSource), 0o600); err != nil {
		t.Fatal(err)
	}
	must("presentation", "edit", id, "--body-file", editedPath)
	if out := must("presentation", "show", id); out != editedSource {
		t.Fatalf("edited source = %q", out)
	}
	if _, err := run("presentation", "create", "mismatch.md", "--title", "Mismatch", "--body-file", sourcePath); err == nil {
		t.Fatal("mismatched Markdown title was accepted")
	}
	must("presentation", "create", "blank.md")
	if out := must("presentation", "show", "blank.md"); !strings.HasPrefix(out, "# blank\n") {
		t.Fatalf("default presentation source = %q", out)
	}
}
