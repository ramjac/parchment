package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
)

func TestNoteEncodingFlagsAndScope(t *testing.T) {
	c := newCLI(t)
	c.must("note", "create", "typed.md", "--no-edit")
	c.must("document", "create", "document.md", "--no-edit")
	path := filepath.Join(c.dir, "plain.txt")
	if err := os.WriteFile(path, []byte("caf\xe9"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invocation := range [][]string{
		{"--encoding", "unknown", "plain.txt"},
		{"--encoding", "windows-1252", "typed.md"},
		{"tui", "--encoding", "utf-8", "document.md"},
		{"tui", "--kind", "note", "--encoding", "utf-8", "missing.txt"},
	} {
		if _, err := c.run(invocation...); err == nil || !strings.Contains(err.Error(), "encoding") {
			t.Fatalf("%v = %v", invocation, err)
		}
	}
	for _, sub := range []bool{false, true} {
		cmd := New(&bytes.Buffer{}, &bytes.Buffer{})
		if sub {
			found, _, err := cmd.Find([]string{"tui"})
			if err != nil {
				t.Fatal(err)
			}
			cmd = found
		}
		if err := cmd.ParseFlags([]string{"--encoding", "windows-1252"}); err != nil {
			t.Fatal(err)
		}
		encoding, err := resolveTUIEncoding(cmd, path, artifact.NoteKind)
		if err != nil || encoding != "windows-1252" {
			t.Fatalf("encoding = %q, %v", encoding, err)
		}
	}
	if _, err := c.run("note", "show", "plain.txt", "--encoding", "windows-1252"); err == nil {
		t.Fatal("encoding option leaked to noninteractive commands")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "caf\xe9" {
		t.Fatal("flag validation rewrote source")
	}
}
