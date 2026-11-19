package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSpreadsheetPrintCLI(t *testing.T) {
	c := newCLI(t)
	c.must("spreadsheet", "create", "budget.md")
	c.must("spreadsheet", "cell", "budget.md", "A1", "12")
	c.must("spreadsheet", "cell", "budget.md", "K60", "=A1*2", "--formula")
	c.must("spreadsheet", "add-sheet", "budget.md", "Other")
	c.must("spreadsheet", "cell", "budget.md", "A1", "other", "--sheet", "Other")
	original, err := os.ReadFile("budget.md")
	if err != nil {
		t.Fatal(err)
	}
	text := c.must("spreadsheet", "print", "budget.md")
	if len(strings.Split(text, "\f")) != 7 || !strings.Contains(text, "24") || !strings.Contains(text, "other") {
		t.Fatalf("unexpected printed workbook: %q", text)
	}
	pdf := c.must("spreadsheet", "print", "budget.md", "--format", "pdf")
	if !strings.HasPrefix(pdf, "%PDF-") || !strings.Contains(pdf, "/Count 7") {
		t.Fatal("PDF did not contain all seven pages")
	}
	c.must("spreadsheet", "print", "budget.md", "--format", "pdf", "-o", "budget.pdf")
	if _, err := c.run("spreadsheet", "print", "budget.md", "--format", "pdf", "-o", "budget.pdf"); err == nil {
		t.Fatal("overwrote PDF without confirmation")
	}
	c.must("spreadsheet", "print", "budget.md", "--format", "pdf", "-o", "budget.pdf", "--force")
	if _, err := c.run("spreadsheet", "print", "budget.md", "--format", "pdf", "-o", "budget.md", "--force"); err == nil {
		t.Fatal("overwrote source")
	}
	if saved, err := os.ReadFile("budget.md"); err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("source changed: %v", err)
	}
}
