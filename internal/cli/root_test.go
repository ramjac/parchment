package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRequiresOneExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	var out, errOut bytes.Buffer
	cmd := New(&out, &errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing file accepted")
	}
	path := filepath.Join(t.TempDir(), "missing.md")
	cmd = New(&out, &errOut)
	cmd.SetArgs([]string{path})
	if err := cmd.Execute(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
}

func TestCLIHelpIsFileOriented(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := New(&out, &errOut)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "parchment <file>") || strings.Contains(out.String(), "--workspace") ||
		strings.Contains(out.String(), "note create") {
		t.Fatalf("unexpected CLI help: %s", out.String())
	}
}
