package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertCommand(t *testing.T) {
	c := newCLI(t)
	run := c.run

	filePath := filepath.Join(t.TempDir(), "plain.md")
	content := "# Heading\n\nSome text here.\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run("convert", filePath, "--kind", "document", "--yes")
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	strData := string(data)
	if !strings.Contains(strData, "parchment-meta") {
		t.Errorf("expected parchment-meta block, got: %s", strData)
	}
	if !strings.Contains(strData, `"kind": "document"`) {
		t.Errorf("expected kind to be document, got: %s", strData)
	}
	if !strings.Contains(strData, "parchment-document") {
		t.Errorf("expected parchment-document block, got: %s", strData)
	}
}

func TestConvertCommandWithoutYesNonInteractive(t *testing.T) {
	c := newCLI(t)
	run := c.run

	filePath := filepath.Join(t.TempDir(), "plain.md")
	content := "# Heading\n\nSome text here.\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run("convert", filePath, "--kind", "document")
	if err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("expected error requires --yes, got: %v", err)
	}
}

func TestConvertCommandInvalidKind(t *testing.T) {
	c := newCLI(t)
	run := c.run

	filePath := filepath.Join(t.TempDir(), "plain.md")
	content := "# Heading\n\nSome text here.\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run("convert", filePath, "--kind", "spreadsheet", "--yes")
	if err == nil || !strings.Contains(err.Error(), "unsupported artifact kind") {
		t.Fatalf("expected error unsupported artifact kind, got: %v", err)
	}
}
