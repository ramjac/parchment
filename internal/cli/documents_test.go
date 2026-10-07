package cli

import (
	"strings"
	"testing"
)

func TestDocumentCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	id := "quarterly.md"
	if out := strings.TrimSpace(must("document", "create", id, "--title", "Quarterly", "--body", "first page", "--header", "{title}", "--columns", "2")); !strings.HasSuffix(out, id) {
		t.Fatalf("create printed %q", out)
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
}
