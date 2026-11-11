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

func TestDocumentExportCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run

	id := "export_doc.md"
	must("document", "create", id, "--body", "# Export Header\n\nParagraph with **bold** text.")

	// Test export with -o inferred extension
	docxOut := "export_doc.docx"
	must("document", "export", id, "-o", docxOut)

	if fi, err := os.Stat(docxOut); err != nil || fi.Size() == 0 {
		t.Fatalf("expected %s to be created and non-empty, err: %v", docxOut, err)
	}

	// Refuse overwrite without --force
	if _, err := run("document", "export", id, "-o", docxOut); err == nil {
		t.Fatal("expected error when overwriting existing file without --force")
	}

	// Overwrite with --force succeeds
	must("document", "export", id, "-o", docxOut, "--force")

	// ODF export with -o inferred extension
	odtOut := "out.odt"
	must("document", "export", id, "-o", odtOut)
	if fi, err := os.Stat(odtOut); err != nil || fi.Size() == 0 {
		t.Fatalf("expected %s to be created and non-empty, err: %v", odtOut, err)
	}

	// Unknown format error
	if _, err := run("document", "export", id, "-o", "out.txt"); err == nil {
		t.Fatal("expected error for unknown extension without --format")
	}

	// Explicit --format docx with custom extension
	customOut := "custom.bin"
	must("document", "export", id, "-o", customOut, "--format", "docx")
	if fi, err := os.Stat(customOut); err != nil || fi.Size() == 0 {
		t.Fatalf("expected %s to be created, err: %v", customOut, err)
	}

	// Pending proposals warning test
	must("document", "propose", id, "--body", "Propose edit", "--description", "Pending test")
	_, errOutput, err := c.runWithStderr("document", "export", id, "-o", "with_proposals.docx")
	if err != nil {
		t.Fatalf("export with pending proposal failed: %v", err)
	}
	if !strings.Contains(errOutput, "warning: 1 pending proposal(s) not exported") {
		t.Fatalf("expected pending proposals warning, got: %q", errOutput)
	}
}

func TestDocumentImportCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run

	id := "src.md"
	must("document", "create", id, "--body", "# Imported Header\n\nSome body text.\n\n- Item 1\n- Item 2")

	docxOut := "src.docx"
	must("document", "export", id, "-o", docxOut)

	target := "dst.md"
	out := strings.TrimSpace(must("document", "import", docxOut, target, "--no-edit"))
	if !strings.HasSuffix(out, target) {
		t.Fatalf("expected stdout to print target path, got: %q", out)
	}

	shown := must("document", "show", target)
	if !strings.Contains(shown, "# Imported Header") || !strings.Contains(shown, "Some body text.") {
		t.Fatalf("unexpected content in imported document:\n%s", shown)
	}

	// Refuse to overwrite existing target
	if _, err := run("document", "import", docxOut, target, "--no-edit"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected target already exists error, got: %v", err)
	}

	// ODF export & import
	odtOut := "src.odt"
	must("document", "export", id, "-o", odtOut)
	targetODT := "dst_odt.md"
	outODT := strings.TrimSpace(must("document", "import", odtOut, targetODT, "--no-edit"))
	if !strings.HasSuffix(outODT, targetODT) {
		t.Fatalf("expected stdout to print target path, got: %q", outODT)
	}
	shownODT := must("document", "show", targetODT)
	if !strings.Contains(shownODT, "# Imported Header") {
		t.Fatalf("unexpected content in imported ODF document:\n%s", shownODT)
	}

	// Unsupported / unknown format
	if _, err := run("document", "import", "sample.xyz", "target3.md", "--no-edit"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}
