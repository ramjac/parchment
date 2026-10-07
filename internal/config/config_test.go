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
	if err := os.WriteFile(userPath, []byte("version = 1\ntheme = \"user\"\nundo_limit = 8\n\n[workspace]\nartifact_directory = \"user-artifacts\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte("version = 1\ntheme = \"workspace\"\nundo_limit = 12\n\n[workspace]\nartifact_directory = \"workspace-artifacts\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARCHMENT_THEME", "environment")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "16")
	t.Setenv("PARCHMENT_ARTIFACT_DIRECTORY", "environment-artifacts")
	settings, err := Load(userPath, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "environment" || settings.UndoLimit != 16 {
		t.Fatalf("settings = %+v", settings)
	}
	if settings.ArtifactDirectory != "environment-artifacts" {
		t.Fatalf("artifact directory = %q", settings.ArtifactDirectory)
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
	for _, path := range []string{"", ".", "..", "../outside", "/absolute"} {
		t.Run(path, func(t *testing.T) {
			if err := validateArtifactDirectory(path); err == nil {
				t.Fatalf("accepted artifact directory %q", path)
			}
		})
	}
}
