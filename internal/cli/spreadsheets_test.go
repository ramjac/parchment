package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
