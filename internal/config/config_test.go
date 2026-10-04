package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.toml")
	if err := os.WriteFile(userPath, []byte("version = 1\ntheme = \"user\"\nundo_limit = 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARCHMENT_THEME", "environment")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "16")
	settings, err := Load(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "environment" || settings.UndoLimit != 16 {
		t.Fatalf("settings = %+v", settings)
	}

	t.Setenv("PARCHMENT_UNDO_LIMIT", "invalid")
	if _, err := Load(userPath); err == nil {
		t.Fatal("invalid environment setting did not fail validation")
	}
	if err := os.WriteFile(userPath, []byte("version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(userPath); err == nil {
		t.Fatal("unsupported config version did not fail")
	}
}

func TestUserConfigPathUsesPerUserParchmentTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config directory: %v", err)
	}
	path, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "parchment", "parchment.toml") {
		t.Fatalf("user config path = %q", path)
	}
}
