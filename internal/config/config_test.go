package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.toml")
	workspacePath := filepath.Join(dir, "workspace.toml")
	if err := os.WriteFile(userPath, []byte("version = 1\ntheme = \"user\"\nundo_limit = 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte("version = 1\ntheme = \"workspace\"\nundo_limit = 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARCHMENT_THEME", "environment")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "16")
	settings, err := Load(userPath, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "environment" || settings.UndoLimit != 16 {
		t.Fatalf("settings = %+v", settings)
	}
	t.Setenv("PARCHMENT_UNDO_LIMIT", "invalid")
	if _, err := Load(userPath, workspacePath); err == nil {
		t.Fatal("invalid environment setting did not fail validation")
	}
	if err := os.WriteFile(workspacePath, []byte("version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(userPath, workspacePath); err == nil {
		t.Fatal("unsupported config version did not fail")
	}
}
