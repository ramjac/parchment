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
	if err := os.WriteFile(userPath, []byte("version = 1\ntheme = \"user\"\nundo_limit = 8\n\n[workspace]\nartifact_dir = \"user/artifacts\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte("version = 1\ntheme = \"workspace\"\nundo_limit = 12\n\n[workspace]\nartifact_dir = \"workspace/artifacts\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARCHMENT_THEME", "environment")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "16")
	t.Setenv("PARCHMENT_ARTIFACT_DIR", "environment/artifacts")
	settings, err := Load(userPath, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "environment" || settings.UndoLimit != 16 || settings.ArtifactDir != "environment/artifacts" {
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

func TestArtifactDirectoryValidation(t *testing.T) {
	for _, invalid := range []string{"", ".", "../outside", "nested/../../outside", "/absolute/path"} {
		if _, err := ValidateArtifactDir(invalid); err == nil {
			t.Errorf("ValidateArtifactDir(%q) succeeded", invalid)
		}
	}
	for _, valid := range []string{"parchment/artifacts", "notes", "nested/../artifacts"} {
		if _, err := ValidateArtifactDir(valid); err != nil {
			t.Errorf("ValidateArtifactDir(%q): %v", valid, err)
		}
	}
}
