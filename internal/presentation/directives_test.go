package presentation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
)

func TestValidateDirectives_Image(t *testing.T) {
	now := time.Now().UTC()
	p := Presentation{
		Artifact: artifact.Artifact{
			ID: "p1", Kind: artifact.PresentationKind, FormatVersion: artifact.FormatVersion,
			CreatedAt: now, ModifiedAt: now,
		},
		Version: FileVersion,
		Images: []artifact.Image{
			{Name: "valid.png", Data: []byte("pngdata")},
		},
		Source: "# Title\n\n## Slide\n\n.image valid.png 100 200\n",
	}

	if err := Validate(p); err != nil {
		t.Fatalf("expected valid presentation with .image, got error: %v", err)
	}

	// Missing image
	p.Source = "# Title\n\n## Slide\n\n.image missing.png 100 200\n"
	if err := Validate(p); err == nil || !strings.Contains(err.Error(), "presentation image \"missing.png\" not found") {
		t.Fatalf("expected missing image validation error, got: %v", err)
	}
}

func TestValidateDirectives_CodeSecurity(t *testing.T) {
	now := time.Now().UTC()
	p := Presentation{
		Artifact: artifact.Artifact{
			ID: "p1", Kind: artifact.PresentationKind, FormatVersion: artifact.FormatVersion,
			CreatedAt: now, ModifiedAt: now,
		},
		Version: FileVersion,
		Source:  "# Title\n\n## Slide\n\n.code /etc/passwd\n",
	}

	// Absolute path
	if err := Validate(p); err == nil || !strings.Contains(err.Error(), "must not be absolute") {
		t.Fatalf("expected absolute path error, got: %v", err)
	}

	// Directory traversal
	p.Source = "# Title\n\n## Slide\n\n.code ../secret.go\n"
	if err := Validate(p); err == nil || !strings.Contains(err.Error(), "cannot traverse outside") {
		t.Fatalf("expected traversal error, got: %v", err)
	}
}

func TestExpandDirectives_Image(t *testing.T) {
	images := []artifact.Image{
		{Name: "diagram.png", Data: []byte("data")},
	}
	src := "## Slide\n\n.image diagram.png 400 300\n\nSome caption"
	got, err := ExpandDirectives(src, "/some/dir", images)
	if err != nil {
		t.Fatalf("ExpandDirectives failed: %v", err)
	}
	want := "## Slide\n\n![diagram.png](diagram.png)\n\nSome caption"
	if got != want {
		t.Fatalf("ExpandDirectives = %q, want %q", got, want)
	}
}

func TestExpandDirectives_Code(t *testing.T) {
	dir := t.TempDir()
	codeContent := `package main

import "fmt"

func main() {
	fmt.Println("Hello") // HL
	fmt.Println("Omitted") // OMIT
	fmt.Println("World")
}
`
	codePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(codePath, []byte(codeContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Full file inclusion
	src := ".code sample.go"
	got, err := ExpandDirectives(src, dir, nil)
	if err != nil {
		t.Fatalf("ExpandDirectives failed: %v", err)
	}
	if !strings.Contains(got, "```go") || !strings.Contains(got, "fmt.Println(\"Hello\")") {
		t.Fatalf("unexpected code output:\n%s", got)
	}
	if strings.Contains(got, "Omitted") {
		t.Fatalf("// OMIT line was not stripped:\n%s", got)
	}
	if strings.Contains(got, "// HL") {
		t.Fatalf("// HL marker was not removed:\n%s", got)
	}

	// Line number range
	srcRange := ".code sample.go 5,7"
	gotRange, err := ExpandDirectives(srcRange, dir, nil)
	if err != nil {
		t.Fatalf("ExpandDirectives range failed: %v", err)
	}
	if !strings.Contains(gotRange, "func main() {") || strings.Contains(gotRange, "package main") {
		t.Fatalf("line range not honored:\n%s", gotRange)
	}

	// Regex bounds /START/,/END/
	srcRegex := ".code sample.go /func main/,/}/"
	gotRegex, err := ExpandDirectives(srcRegex, dir, nil)
	if err != nil {
		t.Fatalf("ExpandDirectives regex failed: %v", err)
	}
	if !strings.Contains(gotRegex, "func main() {") || strings.Contains(gotRegex, "package main") {
		t.Fatalf("regex range not honored:\n%s", gotRegex)
	}

	// Single line regex
	srcSingle := ".code sample.go /package main/"
	gotSingle, err := ExpandDirectives(srcSingle, dir, nil)
	if err != nil {
		t.Fatalf("ExpandDirectives single regex failed: %v", err)
	}
	if !strings.Contains(gotSingle, "package main") || strings.Contains(gotSingle, "func main") {
		t.Fatalf("single regex not honored:\n%s", gotSingle)
	}
}

func TestExpandDirectives_CodeSecuritySymlinkAndSize(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()

	secretFile := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Symlink pointing outside dir
	symlinkPath := filepath.Join(dir, "escaped.go")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatal(err)
	}

	src := ".code escaped.go"
	_, err := ExpandDirectives(src, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "escapes directory") {
		t.Fatalf("expected symlink escape error, got: %v", err)
	}

	// File size exceeding 1 MiB
	largeFile := filepath.Join(dir, "large.go")
	largeData := make([]byte, 1024*1024+10)
	if err := os.WriteFile(largeFile, largeData, 0o600); err != nil {
		t.Fatal(err)
	}

	srcLarge := ".code large.go"
	_, err = ExpandDirectives(srcLarge, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
}
