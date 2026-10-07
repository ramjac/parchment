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
