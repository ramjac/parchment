package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
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
	must("document", "tracking", id, "on")
	if _, err := run("document", "edit", id, "--body", "tracked text"); err == nil {
		t.Fatal("enabled tracking did not require an explicit CLI choice")
	}
	must("document", "edit", id, "--body", "tracked text", "--track")
	changeID := strings.Fields(must("document", "changes", id))[0]
	if out := must("document", "show", id); !strings.Contains(out, "tracked text") {
		t.Fatalf("tracked edit did not change live content: %q", out)
	}
	if out := must("document", "changes", id); !strings.Contains(out, changeID) || !strings.Contains(out, "pending") {
		t.Fatalf("changes = %q", out)
	}
	if out := must("document", "review", id, changeID); !strings.Contains(out, "tracked text") || !strings.Contains(out, "second page") {
		t.Fatalf("review = %q", out)
	}
	must("document", "accept", id, changeID)
	if out := must("document", "show", id); strings.Contains(out, "second page") || !strings.Contains(out, "tracked text") {
		t.Fatalf("accepted document = %q", out)
	}
	if _, err := run("document", "accept", id, changeID); err == nil {
		t.Fatal("accepted change was accepted a second time")
	}
}

func TestDocumentTrackingChoiceForMarkupAndImages(t *testing.T) {
	c := newCLI(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x35\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")
	if err := os.WriteFile("pixel.png", png, 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("document", "create", "doc.md", "--body", "body")
	c.must("document", "tracking", "doc.md", "on")
	for _, operation := range [][]string{
		{"page-break", "doc.md"},
		{"section-break", "doc.md"},
		{"image", "doc.md", "pixel.png"},
	} {
		args := append([]string{"document"}, operation...)
		if _, err := c.run(args...); err == nil || !strings.Contains(err.Error(), "choose --track") {
			t.Fatalf("%v without choice: %v", operation, err)
		}
		c.must(append(args, "--track")...)
		c.must("document", "reject", "doc.md", "--all")
		c.must(append(args, "--track=false")...)
	}
	name := strings.TrimSpace(c.must("document", "images", "doc.md"))
	name = strings.Fields(name)[0]
	if _, err := c.run("document", "image-remove", "doc.md", name, "--strip-references"); err == nil || !strings.Contains(err.Error(), "choose --track") {
		t.Fatalf("image removal without choice: %v", err)
	}
	c.must("document", "image-remove", "doc.md", name, "--strip-references", "--track")
	c.must("document", "reject", "doc.md", "--all")
	if got := c.must("document", "show", "doc.md"); !strings.Contains(got, name) {
		t.Fatal("rejecting image removal did not restore its reference")
	}
	c.must("document", "image-remove", "doc.md", name, "--strip-references", "--track=false")
}

func TestDocumentPrintPDFCLI(t *testing.T) {
	c := newCLI(t)
	c.must("document", "create", "pdf.md", "--body", "# Printable\n\nPDF output.")

	pdf := c.must("document", "print", "pdf.md", "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") {
		t.Fatal("PDF print output is missing the PDF signature")
	}
	if _, err := c.run("document", "print", "pdf.md", "--format", "invalid"); err == nil {
		t.Fatal("unsupported print format was accepted")
	}

	c.must("document", "print", "pdf.md", "--format", "pdf", "-o", "output.pdf")
	repo, err := filerepo.New(filepath.Join(c.home, ".parchment"), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if path, err := repo.LastPDFPath(t.Context()); err != nil || path != filepath.Join(c.dir, "output.pdf") {
		t.Fatalf("remembered PDF path = %q, %v", path, err)
	}
	info, err := os.Stat("output.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("new PDF permissions = %v; want 0600", info.Mode().Perm())
	}
	if _, err := c.run("document", "print", "pdf.md", "--format", "pdf", "-o", "pdf.md", "--force"); err == nil {
		t.Fatal("PDF export overwrote the source document")
	}
	if _, err := c.run("document", "print", "pdf.md", "--format", "pdf", "-o", "output.pdf"); err == nil {
		t.Fatal("existing output was overwritten without --force")
	}
	if err := os.Chmod("output.pdf", 0o640); err != nil {
		t.Fatal(err)
	}
	c.must("document", "print", "pdf.md", "--format", "pdf", "-o", "output.pdf", "--force")
	data, err := os.ReadFile("output.pdf")
	if err != nil || !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatalf("PDF output file is invalid: %v", err)
	}
	info, err = os.Stat("output.pdf")
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("overwritten PDF permissions = %v, %v; want 0640", info, err)
	}

	if err := os.WriteFile("text.out", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run("document", "print", "pdf.md", "-o", "text.out"); err == nil {
		t.Fatal("text output was overwritten without --force")
	}
	if data, err := os.ReadFile("text.out"); err != nil || string(data) != "old" {
		t.Fatalf("refused text overwrite changed output: %v", err)
	}
	c.must("document", "print", "pdf.md", "-o", "text.out", "--force")
	data, err = os.ReadFile("text.out")
	if err != nil || string(data) == "old" {
		t.Fatalf("forced text output did not replace destination: %v", err)
	}
	if err := os.Remove("text.out"); err != nil {
		t.Fatal(err)
	}
	c.must("document", "print", "pdf.md", "-o", "text.out")
	info, err = os.Stat("text.out")
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new text output permissions = %v, %v; want 0600", info, err)
	}
	source, err := os.ReadFile("pdf.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.run("document", "print", "pdf.md", "-o", "pdf.md", "--force"); err == nil {
		t.Fatal("text output overwrote the source document")
	}
	if got, err := os.ReadFile("pdf.md"); err != nil || !bytes.Equal(got, source) {
		t.Fatalf("source document changed: %v", err)
	}
}

func TestConfirmPDFOverwrite(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{" YES \n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
	} {
		var output bytes.Buffer
		got, err := confirmPDFOverwrite(strings.NewReader(tt.input), &output, "report.pdf")
		if err != nil || got != tt.want || !strings.Contains(output.String(), "[y/N]") {
			t.Fatalf("confirmation for %q = %t, %v, prompt %q", tt.input, got, err, output.String())
		}
	}
}

func TestPrintExampleDocumentPDF(t *testing.T) {
	example, err := filepath.Abs("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	c := newCLI(t)
	pdf := c.must("document", "print", example, "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") {
		t.Fatal("example document output is missing the PDF signature")
	}
	printed := c.must("document", "print", example)
	if !strings.Contains(printed, "Planned and completed work") ||
		strings.Contains(printed, "parchment-chart:") {
		t.Fatal("example document did not render its embedded chart")
	}
}

func TestExampleChartsRespondToSheetEdits(t *testing.T) {
	documentPath, err := filepath.Abs("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	presentationPath, err := filepath.Abs("../../examples/presentation.md")
	if err != nil {
		t.Fatal(err)
	}
	newCLI(t)
	repo, settings, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := document.NewService(repo, settings.UndoLimit).Get(t.Context(), documentPath)
	if err != nil {
		t.Fatal(err)
	}
	deck, err := presentation.NewService(repo, settings.UndoLimit).Get(t.Context(), presentationPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name             string
		book             spreadsheet.Workbook
		sheet            string
		anchorColumn     int
		newValue         string
		seriesIndex, row int
		before, after    float64
	}{
		{"document", doc.Workbook, "Effort", 5, "20", 1, 0, 10, 18},
		{"presentation", deck.Workbook, "Revenue", 4, "50", 0, 1, 58, 66},
	} {
		t.Run(tt.name, func(t *testing.T) {
			chart, ok := spreadsheet.ChartAtInWorkbook(&tt.book, tt.sheet, 1, tt.anchorColumn)
			if !ok {
				t.Fatal("example chart is not at its documented sheet anchor")
			}
			data, err := spreadsheet.EvaluateChartInWorkbook(&tt.book, chart)
			if err != nil || len(data.Series) <= tt.seriesIndex ||
				len(data.Series[tt.seriesIndex].Values) <= tt.row ||
				data.Series[tt.seriesIndex].Values[tt.row] != tt.before {
				t.Fatalf("initial chart data: %+v, %v", data, err)
			}
			err = tt.book.SetCell(spreadsheet.SetCellOptions{
				Sheet: tt.sheet, Row: 2, Column: 2, Cell: spreadsheet.Cell{Value: tt.newValue},
			})
			if err != nil {
				t.Fatal(err)
			}
			data, err = spreadsheet.EvaluateChartInWorkbook(&tt.book, chart)
			if err != nil || len(data.Series) <= tt.seriesIndex ||
				len(data.Series[tt.seriesIndex].Values) <= tt.row ||
				data.Series[tt.seriesIndex].Values[tt.row] != tt.after {
				t.Fatalf("chart data after editing source: %+v, %v", data, err)
			}
		})
	}
}

func TestDocumentTrackingKeepsEmbeddedImages(t *testing.T) {
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
	c.must("document", "edit", "report.md", "--body", strings.Replace(shown, "Intro", "Revised intro", 1), "--track")
	c.must("document", "accept", "report.md", "--all")
	accepted := c.must("document", "show", "report.md")
	if !strings.Contains(accepted, "Revised intro") || !strings.Contains(accepted, name) {
		t.Fatalf("accepted body = %q", accepted)
	}
	after, err := os.ReadFile("report.md")
	if err != nil || !strings.Contains(string(after), `"name": "`+name) && !strings.Contains(string(after), `"name":"`+name) {
		t.Fatalf("accepted change dropped the embedded image:\n%s", after)
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

	must("document", "edit", id, "--body", "Tracked edit", "--track")
	_, errOutput, err := c.runWithStderr("document", "export", id, "-o", "with_changes.docx")
	if err != nil {
		t.Fatalf("export with pending changes failed: %v", err)
	}
	if !strings.Contains(errOutput, "warning: 1 pending tracked change(s) not exported") {
		t.Fatalf("expected pending changes warning, got: %q", errOutput)
	}
}

func TestDocumentEmbeddedOfficeExportWarningsAndPDF(t *testing.T) {
	c := newCLI(t)
	repo, settings, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(repo, settings.UndoLimit)
	path := filepath.Join(c.dir, "embedded.md")
	item, err := service.Create(t.Context(), path, document.Draft{
		Body: "![Spreadsheet](parchment-sheet:data-sheet)\n\n![Chart](parchment-chart:revenue-chart)",
	})
	if err != nil {
		t.Fatal(err)
	}
	workbook := spreadsheet.Workbook{
		Sheets: []spreadsheet.Sheet{
			{ID: "data-sheet", Name: "Data", Rows: [][]spreadsheet.Cell{
				{{Value: "Region"}, {Value: "Revenue"}},
				{{Value: "North"}, {Value: "42"}},
			}},
			{ID: "chart-sheet", Name: "Charts", Rows: [][]spreadsheet.Cell{{{}}}},
		},
		Charts: []spreadsheet.Chart{{
			ID: "revenue-chart", PlaceSheet: "Charts", PlaceRow: 1, PlaceColumn: 1,
			Title: "Revenue", Type: "bar", Sheet: "Data",
			FirstRow: 1, FirstColumn: 1, LastRow: 2, LastColumn: 2,
			Header: true, Labels: true,
		}},
	}
	_, err = service.Save(t.Context(), item, document.Draft{
		Body: item.Body, Layout: item.Layout, Images: item.Images, Workbook: workbook,
	})
	if err != nil {
		t.Fatal(err)
	}

	pdfPath := filepath.Join(c.dir, "embedded.pdf")
	c.must("document", "print", path, "--format", "pdf", "-o", pdfPath)
	pdf, err := os.ReadFile(pdfPath)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("embedded chart PDF was not generated: %v", err)
	}

	for _, extension := range []string{"docx", "odt"} {
		output := filepath.Join(c.dir, "embedded."+extension)
		_, warning, err := c.runWithStderr("document", "export", path, "-o", output)
		if err != nil {
			t.Fatalf("export %s: %v", extension, err)
		}
		if got := strings.Count(warning, "editable office objects are unavailable"); got != 2 {
			t.Fatalf("%s approximation warnings = %d, want one per embedded object: %q", extension, got, warning)
		}
	}
}

func TestDocumentOutputsWarnAboutPendingTracking(t *testing.T) {
	c := newCLI(t)
	c.must("document", "create", "doc.md", "--body", "Old body")
	c.must("document", "edit", "doc.md", "--body", "New body", "--track")
	for _, args := range [][]string{
		{"document", "show", "doc.md"},
		{"document", "print", "doc.md"},
		{"document", "print", "doc.md", "--format", "pdf"},
		{"document", "export", "doc.md", "-o", "doc.odt"},
	} {
		out, warning, err := c.runWithStderr(args...)
		if err != nil || !strings.Contains(warning, "pending tracked change(s) not exported") {
			t.Fatalf("%v: warning %q, err %v", args, warning, err)
		}
		if args[1] == "show" || args[1] == "print" && len(args) == 3 {
			if !strings.Contains(out, "New body") || strings.Contains(out, "Old body") {
				t.Fatalf("%v exported incorrect content: %q", args, out)
			}
		}
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
