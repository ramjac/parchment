package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

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
	if _, err := run("spreadsheet", "cell", id, "A1", "=1/0", "--formula"); err == nil {
		t.Fatal("division by zero formula was stored")
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

	// Test export of a specific sheet via --sheet
	must("spreadsheet", "add-sheet", id, "Summary")
	must("spreadsheet", "cell", id, "A1", "Test", "--sheet", "Summary")
	out = must("spreadsheet", "export", id, "--csv", "--sheet", "Summary")
	if out != "Test\n" {
		t.Fatalf("expected %q, got %q", "Test\n", out)
	}

	// Wait, we can test that division by zero formula fails to save entirely, so we shouldn't attempt to add it.
}
