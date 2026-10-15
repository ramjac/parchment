package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PARCHMENT_EDITOR",
		"PARCHMENT_THEME",
		"PARCHMENT_UNDO_LIMIT",
		"PARCHMENT_LOG_LEVEL",
		"PARCHMENT_LOG_FORMAT",
		"PARCHMENT_CONFIG",
	}
	for _, k := range keys {
		if val, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			v := val
			key := k
			t.Cleanup(func() { os.Setenv(key, v) })
		}
	}
}

func TestLoadEmptyPathAndMissingFile(t *testing.T) {
	clearEnv(t)
	expectedDefaults := Settings{
		Editor:    "vi",
		Theme:     "adaptive",
		UndoLimit: 100,
		LogLevel:  "warn",
		LogFormat: "text",
	}

	t.Run("empty path", func(t *testing.T) {
		settings, err := Load("")
		if err != nil {
			t.Fatalf("unexpected error loading empty path: %v", err)
		}
		if settings != expectedDefaults {
			t.Fatalf("got settings %+v, want %+v", settings, expectedDefaults)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		nonExistentPath := filepath.Join(t.TempDir(), "nonexistent.toml")
		settings, err := Load(nonExistentPath)
		if err != nil {
			t.Fatalf("unexpected error loading missing file: %v", err)
		}
		if settings != expectedDefaults {
			t.Fatalf("got settings %+v, want %+v", settings, expectedDefaults)
		}
	})
}

func TestLoadFileParsingAndErrors(t *testing.T) {
	clearEnv(t)

	t.Run("full toml config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "parchment.toml")
		content := `
version = 1
editor = "nvim"
theme = "dark"
undo_limit = 50

[logging]
level = "debug"
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
			Editor:      "nvim",
			Theme:       "dark",
			UndoLimit:   50,
			LogLevel:    "debug",
			LogFormat:   "json",
			LocalBackup: "/tmp/backup",
		}
		if settings != expected {
			t.Fatalf("got %+v, want %+v", settings, expected)
		}
	})

	t.Run("invalid toml", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "parchment.toml")
		if err := os.WriteFile(path, []byte("version = 1\n[invalid toml ==="), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("expected error for invalid TOML, got nil")
		}
	})

	t.Run("unreadable file directory", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := Load(dir); err == nil {
			t.Fatal("expected error when path is a directory, got nil")
		}
	})
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PARCHMENT_EDITOR", "emacs")
	t.Setenv("PARCHMENT_THEME", "light")
	t.Setenv("PARCHMENT_UNDO_LIMIT", "200")
	t.Setenv("PARCHMENT_LOG_LEVEL", "DEBUG")
	t.Setenv("PARCHMENT_LOG_FORMAT", "JSON")

	settings, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := Settings{
		Editor:    "emacs",
		Theme:     "light",
		UndoLimit: 200,
		LogLevel:  "debug",
		LogFormat: "json",
	}

	if settings != expected {
		t.Fatalf("got %+v, want %+v", settings, expected)
	}
}

func TestLoadUndoLimitValidation(t *testing.T) {
	clearEnv(t)

	tests := []struct {
		name    string
		limit   string
		wantErr bool
	}{
		{name: "lower bound valid", limit: "1", wantErr: false},
		{name: "upper bound valid", limit: "10000", wantErr: false},
		{name: "too low invalid", limit: "0", wantErr: true},
		{name: "too high invalid", limit: "10001", wantErr: true},
		{name: "negative invalid", limit: "-5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PARCHMENT_UNDO_LIMIT", tt.limit)
			settings, err := Load("")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && settings.UndoLimit != func() int {
				var v int
				for _, ch := range tt.limit {
					v = v*10 + int(ch-'0')
				}
				return v
			}() {
				t.Fatalf("settings.UndoLimit = %d, want %s", settings.UndoLimit, tt.limit)
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

func TestLoadUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root user bypasses file read permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("file permission 000 is not enforced in this environment")
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when reading unreadable config file, got nil")
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
