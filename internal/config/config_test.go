package config

import (
	"fmt"
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

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if ok {
		os.Unsetenv(key)
		t.Cleanup(func() { os.Setenv(key, old) })
	}
}

func TestLoadEmptyPath(t *testing.T) {
	unsetEnv(t, "PARCHMENT_EDITOR")
	unsetEnv(t, "PARCHMENT_THEME")
	unsetEnv(t, "PARCHMENT_UNDO_LIMIT")
	unsetEnv(t, "PARCHMENT_LOG_LEVEL")
	unsetEnv(t, "PARCHMENT_LOG_FORMAT")

	settings, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") failed: %v", err)
	}
	want := Settings{
		Editor:      "vi",
		Theme:       "adaptive",
		UndoLimit:   100,
		LogLevel:    "warn",
		LogFormat:   "text",
		LocalBackup: "",
	}
	if settings != want {
		t.Fatalf("got %+v, want %+v", settings, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "nonexistent.toml")
	settings, err := Load(missingPath)
	if err != nil {
		t.Fatalf("Load(%q) failed: %v", missingPath, err)
	}
	if settings.Editor != "vi" || settings.Theme != "adaptive" || settings.UndoLimit != 100 {
		t.Fatalf("unexpected settings for missing file: %+v", settings)
	}
}

func TestLoadFullConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parchment.toml")
	content := `version = 1
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
		t.Fatalf("Load failed: %v", err)
	}

	want := Settings{
		Editor:      "nano",
		Theme:       "dark",
		UndoLimit:   50,
		LogLevel:    "info",
		LogFormat:   "json",
		LocalBackup: "/tmp/backup",
	}
	if settings != want {
		t.Fatalf("got %+v, want %+v", settings, want)
	}
}

func TestLoadEnvironmentOverridesAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parchment.toml")
	content := `version = 1
editor = "vi"
theme = "light"
undo_limit = 100
[logging]
level = "warn"
format = "text"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PARCHMENT_EDITOR", "emacs")
	t.Setenv("PARCHMENT_THEME", "solarized")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "250")
	t.Setenv("PARCHMENT_LOG_LEVEL", "DEBUG")
	t.Setenv("PARCHMENT_LOG_FORMAT", "JSON")

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if settings.Editor != "emacs" {
		t.Errorf("Editor = %q, want emacs", settings.Editor)
	}
	if settings.Theme != "solarized" {
		t.Errorf("Theme = %q, want solarized", settings.Theme)
	}
	if settings.UndoLimit != 250 {
		t.Errorf("UndoLimit = %d, want 250", settings.UndoLimit)
	}
	if settings.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", settings.LogLevel)
	}
	if settings.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", settings.LogFormat)
	}
}

func TestLoadUndoLimitValidationBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		shouldErr bool
	}{
		{"min boundary valid", 1, false},
		{"max boundary valid", 10000, false},
		{"below min invalid", 0, true},
		{"above max invalid", 10001, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "parchment.toml")
			content := fmt.Sprintf("version = 1\nundo_limit = %d\n", tt.limit)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			settings, err := Load(path)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("expected error for undo_limit = %d, got nil", tt.limit)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for undo_limit = %d: %v", tt.limit, err)
				}
				if settings.UndoLimit != tt.limit {
					t.Fatalf("settings.UndoLimit = %d, want %d", settings.UndoLimit, tt.limit)
				}
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("malformed toml", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "parchment.toml")
		if err := os.WriteFile(path, []byte("version = [unclosed_array"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("expected error parsing malformed TOML, got nil")
		}
	})

	t.Run("unreadable file path", func(t *testing.T) {
		dirPath := t.TempDir()
		if _, err := Load(dirPath); err == nil {
			t.Fatal("expected error reading directory as config file, got nil")
		}
	})
}
