package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
)

func TestConvertCommand(t *testing.T) {
	c := newCLI(t)

	filePath := filepath.Join(t.TempDir(), "plain.md")
	content := "# Heading\n\nSome text here.\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filePath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	beforeConversion := time.Now().UTC()
	_, err := c.run("convert", filePath, "--yes")
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	afterConversion := time.Now().UTC()

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	converted, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatalf("decode converted file: %v", err)
	}
	if converted.Artifact.Kind != artifact.DocumentKind {
		t.Errorf("kind = %q, want %q", converted.Artifact.Kind, artifact.DocumentKind)
	}
	if !converted.Artifact.CreatedAt.Equal(mtime) {
		t.Errorf("created_at = %s, want file mtime %s", converted.Artifact.CreatedAt, mtime)
	}
	if converted.Artifact.ModifiedAt.Before(beforeConversion) || converted.Artifact.ModifiedAt.After(afterConversion) {
		t.Errorf("modified_at = %s, want current UTC time", converted.Artifact.ModifiedAt)
	}
	if converted.Body != content {
		t.Errorf("body = %q, want %q", converted.Body, content)
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
	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != content {
		t.Fatalf("file changed without confirmation: %q", data)
	}
}

func TestConfirmConversion(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      bool
		wantError bool
	}{
		{name: "yes", input: "yes\n", want: true},
		{name: "case insensitive", input: "Y\n", want: true},
		{name: "no", input: "n\n"},
		{name: "empty input", input: "\n"},
		{name: "end of input", input: ""},
		{name: "read error", input: "", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var input io.Reader = strings.NewReader(test.input)
			if test.wantError {
				input = failingReader{}
			}
			var output bytes.Buffer
			got, err := confirmConversion(input, &output, "file.md", artifact.DocumentKind)
			if (err != nil) != test.wantError {
				t.Fatalf("confirmConversion error = %v, wantError %t", err, test.wantError)
			}
			if err == nil && got != test.want {
				t.Errorf("confirmConversion = %t, want %t", got, test.want)
			}
			if !strings.Contains(output.String(), "Convert file.md to a Parchment document") {
				t.Errorf("prompt = %q", output.String())
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
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
