package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLoadEmptyAndMissingPath(t *testing.T) {
	envVars := []string{
		"PARCHMENT_EDITOR",
		"PARCHMENT_THEME",
		"PARCHMENT_UNDO_LIMIT",
		"PARCHMENT_LOG_LEVEL",
		"PARCHMENT_LOG_FORMAT",
	}
	for _, v := range envVars {
		if orig, ok := os.LookupEnv(v); ok {
			os.Unsetenv(v)
			defer os.Setenv(v, orig)
		}
	}

	// 1. Empty path Load("")
	settings, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") failed: %v", err)
	}
	wantDefault := Settings{
		Editor:    "vi",
		Theme:     "adaptive",
		UndoLimit: 100,
		LogLevel:  "warn",
		LogFormat: "text",
	}
	if settings != wantDefault {
		t.Fatalf("Load(\"\") = %+v, want %+v", settings, wantDefault)
	}

	// 2. Missing configuration file path
	missingPath := filepath.Join(t.TempDir(), "nonexistent.toml")
	settings, err = Load(missingPath)
	if err != nil {
		t.Fatalf("Load(%q) failed for missing file: %v", missingPath, err)
	}
	if settings != wantDefault {
		t.Fatalf("Load(%q) = %+v, want %+v", missingPath, settings, wantDefault)
	}
}

func TestLoadFullTOMLFile(t *testing.T) {
	envVars := []string{
		"PARCHMENT_EDITOR",
		"PARCHMENT_THEME",
		"PARCHMENT_UNDO_LIMIT",
		"PARCHMENT_LOG_LEVEL",
		"PARCHMENT_LOG_FORMAT",
	}
	for _, v := range envVars {
		if orig, ok := os.LookupEnv(v); ok {
			os.Unsetenv(v)
			defer os.Setenv(v, orig)
		}
	}

	path := filepath.Join(t.TempDir(), "full.toml")
	content := `version = 1
editor = "nvim"
theme = "dark"
undo_limit = 200

[logging]
level = "debug"
format = "json"

[backup.local]
destination = "/tmp/backups"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load full toml failed: %v", err)
	}

	want := Settings{
		Editor:      "nvim",
		Theme:       "dark",
		UndoLimit:   200,
		LogLevel:    "debug",
		LogFormat:   "json",
		LocalBackup: "/tmp/backups",
	}

	if settings != want {
		t.Fatalf("settings = %+v, want %+v", settings, want)
	}
}

func TestLoadErrors(t *testing.T) {
	// 1. Malformed TOML syntax
	invalidTomlPath := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(invalidTomlPath, []byte("version = [unclosed bracket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalidTomlPath); err == nil {
		t.Fatal("expected error when parsing malformed TOML file, got nil")
	}

	// 2. Unreadable file permissions
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		unreadablePath := filepath.Join(t.TempDir(), "unreadable.toml")
		if err := os.WriteFile(unreadablePath, []byte("version = 1\n"), 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(unreadablePath); err == nil {
			t.Fatal("expected error when loading unreadable file, got nil")
		}
	}
}

func TestLoadEnvOverridesAndBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parchment.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Test env overrides for editor, log level, log format
	t.Setenv("PARCHMENT_EDITOR", "emacs")
	t.Setenv("PARCHMENT_LOG_LEVEL", "INFO")
	t.Setenv("PARCHMENT_LOG_FORMAT", "JSON")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "50")

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load with env vars failed: %v", err)
	}
	if settings.Editor != "emacs" || settings.LogLevel != "info" || settings.LogFormat != "json" || settings.UndoLimit != 50 {
		t.Fatalf("unexpected settings from env overrides: %+v", settings)
	}

	// Boundary tests for undo_limit
	testCases := []struct {
		name      string
		limitStr  string
		expectErr bool
	}{
		{name: "lower boundary valid (1)", limitStr: "1", expectErr: false},
		{name: "upper boundary valid (10000)", limitStr: "10000", expectErr: false},
		{name: "below lower boundary (0)", limitStr: "0", expectErr: true},
		{name: "above upper boundary (10001)", limitStr: "10001", expectErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PARCHMENT_UNDO_LIMIT", tc.limitStr)
			s, err := Load(path)
			if tc.expectErr && err == nil {
				t.Fatalf("expected error for limit %s, got nil", tc.limitStr)
			}
			if !tc.expectErr {
				if err != nil {
					t.Fatalf("unexpected error for limit %s: %v", tc.limitStr, err)
				}
				if s.UndoLimit != (func() int {
					if tc.limitStr == "1" {
						return 1
					}
					return 10000
				}()) {
					t.Fatalf("expected undo limit %s, got %d", tc.limitStr, s.UndoLimit)
				}
			}
		})
	}
}

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
