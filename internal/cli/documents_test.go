package cli

import (
	"os"
	"strings"
	"testing"
)

func TestDocumentCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	id := "quarterly.md"
	if out := strings.TrimSpace(must("document", "create", id, "--body", "first page", "--header", "{title}", "--columns", "2")); !strings.HasSuffix(out, id) {
		t.Fatalf("create printed %q", out)
	}
	must("document", "page-break", id)
	must("document", "edit", id, "--body", "first page\n\n<!-- parchment:page-break -->\n\nsecond page")

	printed := must("document", "print", id)
	if pages := strings.Count(printed, "\f") + 1; pages < 2 {
		t.Fatalf("printed %d pages:\n%s", pages, printed)
	}
	if !strings.Contains(printed, "quarterly") || !strings.Contains(printed, "second page") {
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
	changeID := strings.TrimSpace(must("document", "propose", id, "--body", "proposed text", "--description", "Update report"))
	if len(changeID) != 32 {
		t.Fatalf("proposal ID = %q", changeID)
	}
	if out := must("document", "show", id); strings.Contains(out, "proposed text") || !strings.Contains(out, "second page") {
		t.Fatalf("proposal changed live content before acceptance: %q", out)
	}
	if out := must("document", "changes", id); !strings.Contains(out, changeID) || !strings.Contains(out, "pending") {
		t.Fatalf("changes = %q", out)
	}
	if out := must("document", "review", id, changeID); !strings.Contains(out, "proposed text") || !strings.Contains(out, "second page") {
		t.Fatalf("review = %q", out)
	}
	must("document", "accept", id, changeID)
	if out := must("document", "show", id); strings.Contains(out, "second page") || !strings.Contains(out, "proposed text") {
		t.Fatalf("accepted document = %q", out)
	}
	if _, err := run("document", "accept", id, changeID); err == nil {
		t.Fatal("accepted proposal was accepted a second time")
	}
}

func TestDocumentProposalKeepsEmbeddedImages(t *testing.T) {
	c := newCLI(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x35\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")
	if err := os.WriteFile("pixel.png", png, 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("document", "create", "report.md", "--body", "Intro")
	name := strings.TrimSpace(c.must("document", "image", "report.md", "pixel.png", "--alt", "pixel"))
	before, err := os.ReadFile("report.md")
	if err != nil || !strings.Contains(string(before), name) {
		t.Fatalf("image not embedded: %v", err)
	}
	shown := c.must("document", "show", "report.md")
	change := strings.TrimSpace(c.must("document", "propose", "report.md", "--body", strings.Replace(shown, "Intro", "Revised intro", 1)))
	c.must("document", "accept", "report.md", change)
	accepted := c.must("document", "show", "report.md")
	if !strings.Contains(accepted, "Revised intro") || !strings.Contains(accepted, name) {
		t.Fatalf("accepted body = %q", accepted)
	}
	after, err := os.ReadFile("report.md")
	if err != nil || !strings.Contains(string(after), `"name": "`+name) && !strings.Contains(string(after), `"name":"`+name) {
		t.Fatalf("accepted proposal dropped the embedded image:\n%s", after)
	}
}
