package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const Version = 1

// Settings is the fully resolved application configuration.
type Settings struct {
	Editor      string
	Theme       string
	UndoLimit   int
	LogLevel    string
	LogFormat   string
	LocalBackup string
}

type fileConfig struct {
	Version   int     `toml:"version"`
	Editor    *string `toml:"editor"`
	Theme     *string `toml:"theme"`
	UndoLimit *int    `toml:"undo_limit"`
	Logging   struct {
		Level  *string `toml:"level"`
		Format *string `toml:"format"`
	} `toml:"logging"`
	Backup struct {
		Local struct {
			Destination *string `toml:"destination"`
		} `toml:"local"`
	} `toml:"backup"`
}

const defaultFile = "version = 1\n"

// Load resolves built-in defaults, the TOML file at path, and environment
// overrides. A missing file leaves the defaults in place.
func Load(path string) (Settings, error) {
	settings := Settings{
		Editor: "vi", Theme: "adaptive", UndoLimit: 100,
		LogLevel: "warn", LogFormat: "text",
	}
	if path != "" {
		cfg, err := read(path)
		if err != nil {
			return Settings{}, err
		}
		apply(&settings, cfg)
	}
	applyEnvironment(&settings)
	if settings.UndoLimit < 1 || settings.UndoLimit > 10000 {
		return Settings{}, errors.New("undo_limit must be between 1 and 10000")
	}
	return settings, nil
}

// Directory returns the per-user Parchment directory, ~/.parchment. It holds
// the configuration file and application state such as autosave recovery
// drafts. It never holds artifacts.
func Directory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".parchment"), nil
}

// Path returns the configuration file path: PARCHMENT_CONFIG when set,
// otherwise parchment.toml in Directory.
func Path() (string, error) {
	if value := os.Getenv("PARCHMENT_CONFIG"); value != "" {
		return filepath.Abs(value)
	}
	dir, err := Directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "parchment.toml"), nil
}

// EnsureDefault creates a minimal versioned configuration file at path, and
// its parent directory with owner-only permissions, when the file is missing.
func EnsureDefault(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	// MkdirAll leaves an existing directory's mode unchanged; keep
	// Parchment's state directory owner-only even if it was pre-created.
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect config directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restrict config directory permissions: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create config %s: %w", path, err)
	}
	if _, err := file.WriteString(defaultFile); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return file.Close()
}

func read(path string) (fileConfig, error) {
	var cfg fileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Version != Version {
		return cfg, fmt.Errorf("config %s has unsupported version %d (want %d)", path, cfg.Version, Version)
	}
	return cfg, nil
}

func apply(settings *Settings, cfg fileConfig) {
	if cfg.Editor != nil {
		settings.Editor = *cfg.Editor
	}
	if cfg.Theme != nil {
		settings.Theme = *cfg.Theme
	}
	if cfg.UndoLimit != nil {
		settings.UndoLimit = *cfg.UndoLimit
	}
	if cfg.Logging.Level != nil {
		settings.LogLevel = *cfg.Logging.Level
	}
	if cfg.Logging.Format != nil {
		settings.LogFormat = *cfg.Logging.Format
	}
	if cfg.Backup.Local.Destination != nil {
		settings.LocalBackup = *cfg.Backup.Local.Destination
	}
}

func applyEnvironment(settings *Settings) {
	if value, ok := os.LookupEnv("PARCHMENT_EDITOR"); ok {
		settings.Editor = value
	}
	if value, ok := os.LookupEnv("PARCHMENT_THEME"); ok {
		settings.Theme = value
	}
	if value, ok := os.LookupEnv("PARCHMENT_UNDO_LIMIT"); ok {
		if limit, err := strconv.Atoi(value); err == nil {
			settings.UndoLimit = limit
		} else {
			settings.UndoLimit = 0
		}
	}
	if value, ok := os.LookupEnv("PARCHMENT_LOG_LEVEL"); ok {
		settings.LogLevel = strings.ToLower(value)
	}
	if value, ok := os.LookupEnv("PARCHMENT_LOG_FORMAT"); ok {
		settings.LogFormat = strings.ToLower(value)
	}
}
