package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLoadPrecedenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parchment.toml")
	if err := os.WriteFile(path, []byte("version = 1\ntheme = \"file\"\nundo_limit = 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "file" || settings.UndoLimit != 12 {
		t.Fatalf("settings = %+v", settings)
	}
	t.Setenv("PARCHMENT_THEME", "environment")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "16")
	settings, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "environment" || settings.UndoLimit != 16 {
		t.Fatalf("settings = %+v", settings)
	}
	t.Setenv("PARCHMENT_UNDO_LIMIT", "invalid")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid environment setting did not fail validation")
	}
	if err := os.WriteFile(path, []byte("version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unsupported config version did not fail")
	}
}

func TestLoadEmptyPath(t *testing.T) {
	// Ensure env vars that might affect defaults are unset or cleared
	for _, env := range []string{"PARCHMENT_EDITOR", "PARCHMENT_THEME", "PARCHMENT_UNDO_LIMIT", "PARCHMENT_LOG_LEVEL", "PARCHMENT_LOG_FORMAT"} {
		if err := os.Unsetenv(env); err != nil {
			t.Fatal(err)
		}
	}

	settings, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error loading empty path: %v", err)
	}
	if settings.Editor != "vi" || settings.Theme != "adaptive" || settings.UndoLimit != 100 || settings.LogLevel != "warn" || settings.LogFormat != "text" {
		t.Fatalf("unexpected default settings for empty path: %+v", settings)
	}
}

func TestLoadMissingFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "nonexistent.toml")
	settings, err := Load(missingPath)
	if err != nil {
		t.Fatalf("unexpected error loading missing file: %v", err)
	}
	if settings.Editor != "vi" || settings.UndoLimit != 100 {
		t.Fatalf("expected defaults when config file is missing, got: %+v", settings)
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions differ on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when reading unreadable config file, got nil")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(path, []byte("this is not valid toml = ="), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error parsing invalid TOML config file, got nil")
	}
}

func TestLoadFullConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "full.toml")
	content := `
version = 1
editor = "nano"
theme = "dark"
undo_limit = 50

[logging]
level = "info"
format = "json"

[backup.local]
destination = "/tmp/backup"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := Settings{
		Editor:      "nano",
		Theme:       "dark",
		UndoLimit:   50,
		LogLevel:    "info",
		LogFormat:   "json",
		LocalBackup: "/tmp/backup",
	}
	if settings != expected {
		t.Fatalf("settings mismatch: got %+v, want %+v", settings, expected)
	}
}

func TestLoadEnvironmentOverridesAll(t *testing.T) {
	t.Setenv("PARCHMENT_EDITOR", "emacs")
	t.Setenv("PARCHMENT_THEME", "light")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "200")
	t.Setenv("PARCHMENT_LOG_LEVEL", "DEBUG")
	t.Setenv("PARCHMENT_LOG_FORMAT", "JSON")

	settings, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.Editor != "emacs" || settings.Theme != "light" || settings.UndoLimit != 200 || settings.LogLevel != "debug" || settings.LogFormat != "json" {
		t.Fatalf("unexpected settings with env overrides: %+v", settings)
	}
}

func TestLoadUndoLimitValidationBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounds.toml")

	// Test lower bound violation (< 1)
	if err := os.WriteFile(path, []byte("version = 1\nundo_limit = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for undo_limit < 1")
	}

	// Test upper bound violation (> 10000)
	if err := os.WriteFile(path, []byte("version = 1\nundo_limit = 10001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for undo_limit > 10000")
	}
}

func TestPathAndEnsureDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PARCHMENT_CONFIG", "")
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".parchment", "parchment.toml"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if err := EnsureDefault(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != defaultFile {
		t.Fatalf("default config = %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config directory = %v, %v", info, err)
	}
	if err := os.WriteFile(path, []byte("version = 1\ntheme = \"kept\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefault(path); err != nil {
		t.Fatal(err)
	}
	if settings, err := Load(path); err != nil || settings.Theme != "kept" {
		t.Fatalf("existing config was replaced: %+v, %v", settings, err)
	}
	override := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("PARCHMENT_CONFIG", override)
	if path, err := Path(); err != nil || path != override {
		t.Fatalf("override path = %q, %v", path, err)
	}
}

func TestEnsureDefaultRestrictsExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), ".parchment")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefault(filepath.Join(dir, "parchment.toml")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config directory mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestEnsureDefaultConcurrentLaunchesSeeCompleteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".parchment")
	path := filepath.Join(dir, "parchment.toml")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := EnsureDefault(path); err != nil {
				errs <- err
				return
			}
			if _, err := Load(path); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("config directory entries = %v, %v", entries, err)
	}
}
