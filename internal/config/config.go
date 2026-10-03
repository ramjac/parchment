package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const Version = 1

// Settings is the fully resolved application configuration.
type Settings struct {
	Editor             string
	Theme              string
	UndoLimit          int
	WorkspacePath      string
	WorkspaceDiscovery string
	LogLevel           string
	LogFormat          string
	LocalBackup        string
}

type fileConfig struct {
	Version   int     `toml:"version"`
	Editor    *string `toml:"editor"`
	Theme     *string `toml:"theme"`
	UndoLimit *int    `toml:"undo_limit"`
	Workspace struct {
		Path      *string `toml:"path"`
		Discovery *string `toml:"discovery"`
	} `toml:"workspace"`
	Logging struct {
		Level  *string `toml:"level"`
		Format *string `toml:"format"`
	} `toml:"logging"`
	Backup struct {
		Local struct {
			Destination *string `toml:"destination"`
		} `toml:"local"`
	} `toml:"backup"`
}

// Load resolves user then workspace TOML, followed by environment overrides.
func Load(userPath, workspacePath string) (Settings, error) {
	settings := Settings{
		Editor: "vi", Theme: "adaptive", UndoLimit: 100, WorkspaceDiscovery: "parents",
		LogLevel: "warn", LogFormat: "text",
	}
	for _, path := range []string{userPath, workspacePath} {
		if path == "" {
			continue
		}
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
	if settings.WorkspaceDiscovery != "parents" && settings.WorkspaceDiscovery != "disabled" {
		return Settings{}, fmt.Errorf("unsupported workspace discovery mode %q", settings.WorkspaceDiscovery)
	}
	return settings, nil
}

// UserConfigPath returns the platform-appropriate user configuration path.
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "parchment", "config.toml"), nil
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
	if cfg.Workspace.Path != nil {
		settings.WorkspacePath = *cfg.Workspace.Path
	}
	if cfg.Workspace.Discovery != nil {
		settings.WorkspaceDiscovery = *cfg.Workspace.Discovery
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
