package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestDocumentCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	root := t.TempDir()
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
	id := strings.TrimSpace(must("document", "create", "Quarterly", "--body", "first page", "--header", "{title}", "--columns", "2"))
	if len(id) != 32 {
		t.Fatalf("created ID = %q", id)
	}
	must("document", "page-break", id)
	must("document", "edit", id, "--body", "first page\n\n<!-- parchment:page-break -->\n\nsecond page")
	must("document", "tag-add", id, "finance")

	printed := must("document", "print", id)
	if pages := strings.Count(printed, "\f") + 1; pages < 2 {
		t.Fatalf("printed %d pages:\n%s", pages, printed)
	}
	if !strings.Contains(printed, "Quarterly") || !strings.Contains(printed, "second page") {
		t.Fatalf("print output = %q", printed)
	}
	if out := must("document", "layout", id); !strings.Contains(out, "columns: 2") {
		t.Fatalf("layout = %q", out)
	}
	if out := must("document", "layout", id, "--page-size", "a4", "--orientation", "landscape"); !strings.Contains(out, "page-size: a4") {
		t.Fatalf("layout = %q", out)
	}
	if _, err := run("document", "layout", id, "--columns", "9"); err == nil {
		t.Fatal("invalid column count was accepted")
	}
	if out := must("document", "search", "finance"); !strings.Contains(out, id) {
		t.Fatalf("search = %q", out)
	}
	if out := must("document", "list"); !strings.Contains(out, id) {
		t.Fatalf("list = %q", out)
	}
	changeID := strings.TrimSpace(must("document", "propose", id, "--title", "Quarterly proposal", "--body", "proposed text", "--description", "Update report"))
	if len(changeID) != 32 {
		t.Fatalf("proposal ID = %q", changeID)
	}
	if out := must("document", "show", id); !strings.Contains(out, "# Quarterly\n") || !strings.Contains(out, "second page") {
		t.Fatalf("proposal changed live content before acceptance: %q", out)
	}
	if out := must("document", "changes", id); !strings.Contains(out, changeID) || !strings.Contains(out, "pending") {
		t.Fatalf("changes = %q", out)
	}
	if out := must("document", "review", id, changeID); !strings.Contains(out, "proposed text") || !strings.Contains(out, "second page") {
		t.Fatalf("review = %q", out)
	}
	must("document", "accept", id, changeID)
	if out := must("document", "show", id); !strings.Contains(out, "# Quarterly proposal\n") || !strings.Contains(out, "proposed text") {
		t.Fatalf("accepted document = %q", out)
	}
	if _, err := run("document", "accept", id, changeID); err == nil {
		t.Fatal("accepted proposal was accepted a second time")
	}
	if out := must("note", "list"); strings.Contains(out, id) {
		t.Fatalf("document appeared in note list: %q", out)
	}
	if _, err := run("document", "delete", id); err == nil {
		t.Fatal("delete without --yes succeeded")
	}
	must("document", "delete", id, "--yes")
	if out := must("document", "list"); strings.Contains(out, id) {
		t.Fatalf("deleted document still listed: %q", out)
	}
}
