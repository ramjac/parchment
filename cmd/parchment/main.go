package main

import (
	"fmt"
	"log/slog"
	"os"

	"example.com/parchment/internal/cli"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/logging"
)

func main() {
	configPath, err := config.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to get config path:", err)
		os.Exit(1)
	}

	if os.Getenv("PARCHMENT_CONFIG") == "" {
		if err := config.EnsureDefault(configPath); err != nil {
			fmt.Fprintln(os.Stderr, "failed to ensure default config:", err)
			os.Exit(1)
		}
	} else if _, err := os.Stat(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "PARCHMENT_CONFIG: %v\n", err)
		os.Exit(1)
	}

	settings, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to load config:", err)
		os.Exit(1)
	}

	stateDir, err := config.Directory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to get state directory:", err)
		os.Exit(1)
	}

	logger, closer, err := logging.Setup(stateDir, settings.LogLevel, settings.LogFormat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to setup logging:", err)
		os.Exit(1)
	}
	defer closer()
	slog.SetDefault(logger)

	root := cli.New(os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		closer() // explicitly close before os.Exit
		os.Exit(1)
	}
}
