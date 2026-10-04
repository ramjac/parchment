package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestSpreadsheetCLIFormulasAndGridOperations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("PARCHMENT_WORKSPACE", "")
	root := t.TempDir()
	input := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(input, []byte("Item,Count\nApples,4\nOranges,3\n"), 0o600); err != nil {
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
	if err := os.WriteFile(input, []byte("Name\nbad\xff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("spreadsheet", "create", "Invalid", "--csv-file", input); err == nil ||
		!strings.Contains(err.Error(), "valid UTF-8") || out != "" {
		t.Fatalf("invalid UTF-8 CSV import = %q, %v", out, err)
	}
	if out := must("spreadsheet", "list"); strings.Contains(out, "Invalid") {
		t.Fatalf("failed CSV import persisted an artifact: %q", out)
	}
	if err := os.WriteFile(input, []byte("Item,Count\nApples,4\nOranges,3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(must("spreadsheet", "create", "Fruit", "--csv-file", input))
	if len(id) != 32 {
		t.Fatalf("spreadsheet ID = %q", id)
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
	for _, value := range []string{`"title": "Fruit"`, `"kind": "spreadsheet"`, `"formula": "=C3+C4"`, `"name": "Sheet1"`, `"name": "Summary"`} {
		if !strings.Contains(shown, value) {
			t.Fatalf("workbook JSON missing %q:\n%s", value, shown)
		}
	}
	if out := must("spreadsheet", "list"); !strings.Contains(out, id) {
		t.Fatalf("spreadsheet list = %q", out)
	}
	if _, err := run("spreadsheet", "cell", id, "A1", "=1/0", "--formula"); err == nil {
		t.Fatal("division by zero formula was stored")
	}
	if _, err := run("spreadsheet", "delete", id); err == nil {
		t.Fatal("delete without --yes succeeded")
	}
	must("spreadsheet", "delete", id, "--yes")
	if out := must("spreadsheet", "list"); strings.Contains(out, id) {
		t.Fatalf("deleted spreadsheet still listed: %q", out)
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
