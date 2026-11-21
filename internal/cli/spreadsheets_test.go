package cli

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestSpreadsheetChartsCLI(t *testing.T) {
	c := newCLI(t)
	c.must("spreadsheet", "create", "chart.md")
	c.must("spreadsheet", "cell", "chart.md", "A1", "3")
	c.must("spreadsheet", "cell", "chart.md", "A2", "=A1*2", "--formula")
	c.must("spreadsheet", "cell", "chart.md", "B1", "4")
	c.must("spreadsheet", "cell", "chart.md", "B2", "5")
	if _, err := c.run("spreadsheet", "chart", "create", "chart.md", "C1", "pie", "A1:A2"); err == nil {
		t.Fatal("pie chart creation succeeded")
	}
	if _, err := c.run("spreadsheet", "chart", "create", "chart.md", "C1", "bar", "A1:B2"); err == nil {
		t.Fatal("multi-series bar chart creation succeeded")
	}
	id := strings.TrimSpace(c.must("spreadsheet", "chart", "create", "chart.md", "C1", "bar", "A1:A2", "--title", "Values"))
	svg := c.must("spreadsheet", "chart", "export", "chart.md", id)
	if err := xml.Unmarshal([]byte(svg), new(any)); err != nil {
		t.Fatalf("invalid SVG: %v", err)
	}
	c.must("spreadsheet", "chart", "edit", "chart.md", id, "--type", "line")
	if _, err := c.run("spreadsheet", "chart", "edit", "chart.md", id, "--type", "pie"); err == nil {
		t.Fatal("pie chart edit succeeded")
	}
	c.must("spreadsheet", "chart", "export", "chart.md", id, "--format", "pdf", "-o", "chart.pdf")
	if _, err := c.run("spreadsheet", "chart", "export", "chart.md", id, "--format", "pdf", "-o", "chart.pdf"); err == nil {
		t.Fatal("overwriting a chart PDF without force succeeded")
	}
	for _, format := range []string{"svg", "pdf"} {
		output := filepath.Join(t.TempDir(), "new."+format)
		c.must("spreadsheet", "chart", "export", "chart.md", id, "--format", format, "-o", output, "--force")
		if info, err := os.Stat(output); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("forced export to new %s path: info=%v, err=%v", format, info, err)
		}
	}
	if _, err := c.run("spreadsheet", "chart", "export", "chart.md", id, "-o", "chart.md", "--force"); err == nil {
		t.Fatal("overwriting the workbook succeeded")
	}
	c.must("spreadsheet", "chart", "delete", "chart.md", id)
	if _, err := c.run("spreadsheet", "chart", "edit", "chart.md", id, "--title", "Deleted"); err == nil {
		t.Fatal("deleted chart remains")
	}
}

func TestSpreadsheetSpanCLI(t *testing.T) {
	c := newCLI(t)
	c.must("spreadsheet", "create", "span.md")
	c.must("spreadsheet", "cell", "span.md", "A1", "42")
	c.must("spreadsheet", "span", "span.md", "A1:B2")
	if out := c.must("spreadsheet", "cell", "span.md", "A2"); strings.TrimSpace(out) != "" {
		t.Fatalf("covered cell = %q", out)
	}
	if _, err := c.run("spreadsheet", "cell", "span.md", "B2", "lost"); err == nil {
		t.Fatal("covered cell accepted a write")
	}
	if _, err := c.run("spreadsheet", "unspan", "span.md", "B2"); err == nil {
		t.Fatal("covered cell accepted as an anchor")
	}
	if out := c.must("spreadsheet", "export", "span.md", "--csv"); out != "42,\n,\n" {
		t.Fatalf("CSV did not preserve blank covered cells: %q", out)
	}
	c.must("spreadsheet", "unspan", "span.md", "A1")
	c.must("spreadsheet", "cell", "span.md", "B2", "now editable")
}

func TestSpreadsheetCLIFormulasAndGridOperations(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	input := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(input, []byte("Item,Count\nApples,4\nOranges,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(input, []byte("Name\nbad\xff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("spreadsheet", "create", "invalid.md", "--csv-file", input); err == nil ||
		!strings.Contains(err.Error(), "valid UTF-8") || out != "" {
		t.Fatalf("invalid UTF-8 CSV import = %q, %v", out, err)
	}
	if _, err := os.Stat("invalid.md"); !os.IsNotExist(err) {
		t.Fatalf("failed CSV import created a file: %v", err)
	}
	if err := os.WriteFile(input, []byte("Item,Count\nApples,4\nOranges,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "fruit.md"
	if out := strings.TrimSpace(must("spreadsheet", "create", id, "--csv-file", input)); !strings.HasSuffix(out, id) {
		t.Fatalf("create printed %q", out)
	}
	if out := must("spreadsheet", "cell", id, "B2"); strings.TrimSpace(out) != "4" {
		t.Fatalf("cell B2 = %q", out)
	}
	must("spreadsheet", "cell", id, "B4", "=B2+B3", "--formula")
	if out := must("spreadsheet", "cell", id, "B4"); !strings.Contains(out, "= B2+B3") || !strings.HasSuffix(strings.TrimSpace(out), "7") {
		t.Fatalf("formula result = %q", out)
	}
	must("spreadsheet", "insert-row", id, "2")
	if out := must("spreadsheet", "cell", id, "B5"); !strings.Contains(out, "= B3+B4") || !strings.HasSuffix(strings.TrimSpace(out), "7") {
		t.Fatalf("formula after row insertion = %q", out)
	}
	must("spreadsheet", "insert-column", id, "2")
	if out := must("spreadsheet", "cell", id, "C5"); !strings.Contains(out, "= C3+C4") || !strings.HasSuffix(strings.TrimSpace(out), "7") {
		t.Fatalf("formula after column insertion = %q", out)
	}
	must("spreadsheet", "add-sheet", id, "Summary")
	must("spreadsheet", "cell", id, "A1", "Totals", "--sheet", "Summary")
	if out := must("spreadsheet", "cell", id, "A1", "--sheet", "Summary"); strings.TrimSpace(out) != "Totals" {
		t.Fatalf("cell in named sheet = %q", out)
	}
	shown := must("spreadsheet", "show", id)
	for _, value := range []string{`"kind": "spreadsheet"`, `"formula": "=C3+C4"`, `"name": "Sheet1"`, `"name": "Summary"`} {
		if !strings.Contains(shown, value) {
			t.Fatalf("workbook JSON missing %q:\n%s", value, shown)
		}
	}
	must("spreadsheet", "cell", id, "A1", "=1/0", "--formula")
	if out := must("spreadsheet", "cell", id, "A1"); !strings.Contains(out, "#DIV/0!") {
		t.Fatalf("division by zero was not displayed: %q", out)
	}
}

func TestSpreadsheetFormulaResultsInCLIAndCSV(t *testing.T) {
	c := newCLI(t)
	c.must("spreadsheet", "create", "values.md")
	c.must("spreadsheet", "cell", "values.md", "A1", `=IF(1=1,"yes","no")`, "--formula")
	c.must("spreadsheet", "cell", "values.md", "B1", `=1/0`, "--formula")
	if out := c.must("spreadsheet", "cell", "values.md", "A1"); !strings.HasSuffix(strings.TrimSpace(out), "yes") {
		t.Fatalf("string formula output = %q", out)
	}
	if out := c.must("spreadsheet", "export", "values.md", "--csv"); strings.TrimSpace(out) != "yes,#DIV/0!" {
		t.Fatalf("CSV formula output = %q", out)
	}
}

func TestSpreadsheetSpillCLIAndCSV(t *testing.T) {
	c := newCLI(t)
	c.must("spreadsheet", "create", "values.md")
	c.must("spreadsheet", "cell", "values.md", "A1", "=SEQUENCE(2,2)", "--formula")
	if out := strings.TrimSpace(c.must("spreadsheet", "cell", "values.md", "B2")); out != "4" {
		t.Fatalf("spill cell = %q", out)
	}
	if out := strings.TrimSpace(c.must("spreadsheet", "export", "values.md", "--csv")); out != "1,2\n3,4" {
		t.Fatalf("spill CSV = %q", out)
	}
	if _, err := c.run("spreadsheet", "cell", "values.md", "B2", "blocked"); err == nil {
		t.Fatal("editing spill through CLI succeeded")
	}
}

func TestReadSpreadsheetCSVEnforcesInputAndDimensionLimits(t *testing.T) {
	if _, err := readSpreadsheetCSV(strings.NewReader(strings.Repeat("x\n", spreadsheet.MaxRows+1)), 1<<20); err == nil ||
		!strings.Contains(err.Error(), "rows") {
		t.Fatalf("excess CSV rows returned %v", err)
	}
	if _, err := readSpreadsheetCSV(strings.NewReader(strings.Repeat(",", spreadsheet.MaxColumns)), 1<<20); err == nil ||
		!strings.Contains(err.Error(), "columns") {
		t.Fatalf("excess CSV columns returned %v", err)
	}
	if _, err := readSpreadsheetCSV(strings.NewReader("123456789"), 8); err == nil ||
		!strings.Contains(err.Error(), "larger than 8 bytes") {
		t.Fatalf("oversized CSV input returned %v", err)
	}
	rows, err := readSpreadsheetCSV(strings.NewReader("A,B\n1,2\n"), 1024)
	if err != nil || len(rows) != 2 || rows[1][1].Value != "2" {
		t.Fatalf("valid CSV rows = %+v, %v", rows, err)
	}
}

func TestSpreadsheetCSVExport(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	id := "export.md"

	input := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(input, []byte("Item,Cost,Quantity\nApple,1.5,4\nOrange,2.0,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	must("spreadsheet", "create", id, "--csv-file", input)
	must("spreadsheet", "cell", id, "D1", "Total")
	must("spreadsheet", "cell", id, "D2", "=B2*C2", "--formula")
	must("spreadsheet", "cell", id, "D3", "=B3*C3", "--formula")

	// Test --csv is required
	if _, err := run("spreadsheet", "export", id); err == nil || !strings.Contains(err.Error(), "--csv is required") {
		t.Fatalf("expected --csv required error, got %v", err)
	}

	// Test export to stdout
	out := must("spreadsheet", "export", id, "--csv")
	expected := "Item,Cost,Quantity,Total\nApple,1.5,4,6\nOrange,2.0,3,6\n"
	if out != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}

	// Test export with -o
	output := filepath.Join(t.TempDir(), "output.csv")
	must("spreadsheet", "export", id, "--csv", "-o", output)
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expected {
		t.Fatalf("expected %q in file, got %q", expected, string(content))
	}

	// Test -o without --force on existing file
	if _, err := run("spreadsheet", "export", id, "--csv", "-o", output); err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("expected file exists error, got %v", err)
	}

	// Test -o with --force
	must("spreadsheet", "export", id, "--csv", "-o", output, "--force")
	content, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expected {
		t.Fatalf("expected %q in file after force, got %q", expected, string(content))
	}

	// Force must not replace the source workbook.
	if _, err := run("spreadsheet", "export", id, "--csv", "-o", id, "--force"); err == nil ||
		!strings.Contains(err.Error(), "cannot overwrite the source") {
		t.Fatalf("expected source overwrite error, got %v", err)
	}
	if out := must("spreadsheet", "show", id); !strings.Contains(out, "parchment-spreadsheet") {
		t.Fatalf("source workbook was replaced: %q", out)
	}

	// Test export of a specific sheet via --sheet
	must("spreadsheet", "add-sheet", id, "Summary")
	must("spreadsheet", "cell", id, "A1", "Test", "--sheet", "Summary")
	out = must("spreadsheet", "export", id, "--csv", "--sheet", "Summary")
	if out != "Test\n" {
		t.Fatalf("expected %q, got %q", "Test\n", out)
	}

	// Wait, we can test that division by zero formula fails to save entirely, so we shouldn't attempt to add it.
}

func TestSpreadsheetCLI_Deletions(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	id := "deletions.md"

	// Create with basic dimensions 3x3 + Sheet2
	must("spreadsheet", "create", id)
	must("spreadsheet", "cell", id, "A1", "Val")
	must("spreadsheet", "cell", id, "B2", "Val")
	must("spreadsheet", "cell", id, "C3", "Val")
	must("spreadsheet", "add-sheet", id, "Sheet2")

	must("spreadsheet", "delete-row", id, "2")
	must("spreadsheet", "delete-column", id, "2")
	must("spreadsheet", "delete-sheet", id, "Sheet2")

	// Out of bounds checking
	if _, err := run("spreadsheet", "delete-row", id, "999"); err == nil {
		t.Fatal("expected error deleting out of bounds row")
	}
	if _, err := run("spreadsheet", "delete-column", id, "999"); err == nil {
		t.Fatal("expected error deleting out of bounds column")
	}
	if _, err := run("spreadsheet", "delete-sheet", id, "Sheet1"); err == nil {
		t.Fatal("expected error deleting the only sheet")
	}
}
