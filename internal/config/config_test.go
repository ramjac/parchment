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

func TestLoadEmptyPathAndMissingFile(t *testing.T) {
	// Empty path should return defaults
	settings, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") failed: %v", err)
	}
	expectedDefaults := Settings{
		Editor:    "vi",
		Theme:     "adaptive",
		UndoLimit: 100,
		LogLevel:  "warn",
		LogFormat: "text",
	}
	if settings != expectedDefaults {
		t.Fatalf("Load(\"\") = %+v, want %+v", settings, expectedDefaults)
	}

	// Missing file path should return defaults without error
	missingPath := filepath.Join(t.TempDir(), "nonexistent.toml")
	settings, err = Load(missingPath)
	if err != nil {
		t.Fatalf("Load(%q) failed: %v", missingPath, err)
	}
	if settings != expectedDefaults {
		t.Fatalf("Load(%q) = %+v, want %+v", missingPath, settings, expectedDefaults)
	}
}

func TestLoadFileErrors(t *testing.T) {
	// Unreadable directory as path
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatalf("expected error loading directory as file path, got nil")
	}

	// Malformed TOML file
	badToml := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(badToml, []byte("invalid = [ [ ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(badToml); err == nil {
		t.Fatalf("expected error loading invalid TOML, got nil")
	}
}

func TestLoadAllFieldsAndEnvironment(t *testing.T) {
	tomlContent := `
version = 1
editor = "emacs"
theme = "dark"
undo_limit = 50

[logging]
level = "info"
format = "json"

[backup.local]
destination = "/tmp/backup"
`
	path := filepath.Join(t.TempDir(), "parchment.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	want := Settings{
		Editor:      "emacs",
		Theme:       "dark",
		UndoLimit:   50,
		LogLevel:    "info",
		LogFormat:   "json",
		LocalBackup: "/tmp/backup",
	}
	if settings != want {
		t.Fatalf("settings = %+v, want %+v", settings, want)
	}

	// Override all with environment variables
	t.Setenv("PARCHMENT_EDITOR", "nano")
	t.Setenv("PARCHMENT_THEME", "light")
	t.Setenv("PARCHMENT_LOG_LEVEL", "DEBUG")
	t.Setenv("PARCHMENT_LOG_FORMAT", "TEXT")

	settings, err = Load(path)
	if err != nil {
		t.Fatalf("Load with env overrides failed: %v", err)
	}
	wantEnv := Settings{
		Editor:      "nano",
		Theme:       "light",
		UndoLimit:   50,
		LogLevel:    "debug",
		LogFormat:   "text",
		LocalBackup: "/tmp/backup",
	}
	if settings != wantEnv {
		t.Fatalf("settings with env = %+v, want %+v", settings, wantEnv)
	}
}

func TestLoadUndoLimitValidationBoundaries(t *testing.T) {
	dir := t.TempDir()

	testCases := []struct {
		name      string
		content   string
		envVal    string
		expectErr bool
		wantLimit int
	}{
		{name: "valid lower bound 1", content: "version = 1\nundo_limit = 1\n", wantLimit: 1},
		{name: "valid upper bound 10000", content: "version = 1\nundo_limit = 10000\n", wantLimit: 10000},
		{name: "invalid below bound 0", content: "version = 1\nundo_limit = 0\n", expectErr: true},
		{name: "invalid above bound 10001", content: "version = 1\nundo_limit = 10001\n", expectErr: true},
		{name: "invalid env non-numeric", content: "version = 1\n", envVal: "invalid", expectErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.envVal != "" {
				t.Setenv("PARCHMENT_UNDO_LIMIT", tc.envVal)
			}

			settings, err := Load(path)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error for case %q, got nil", tc.name)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for case %q: %v", tc.name, err)
				}
				if settings.UndoLimit != tc.wantLimit {
					t.Fatalf("case %q: UndoLimit = %d, want %d", tc.name, settings.UndoLimit, tc.wantLimit)
				}
			}
		})
	}
}
