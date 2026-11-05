package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Setup configures file-based slog logging according to the provided level and format.
// It returns a configured *slog.Logger, a closer function to cleanly sync and close the log file,
// and an error if setup fails.
// When levelStr is "off", it returns a logger that discards all log events.
func Setup(dir string, levelStr string, format string) (*slog.Logger, func() error, error) {
	if levelStr == "off" {
		handler := slog.NewTextHandler(io.Discard, nil)
		return slog.New(handler), func() error { return nil }, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return nil, nil, fmt.Errorf("invalid log level %q", levelStr)
	}

	logPath := filepath.Join(dir, "parchment.log")

	// Create or open the file for appending.
	file, err := openOrRotate(logPath)
	if err != nil {
		return nil, nil, err
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(file, opts)
	} else {
		handler = slog.NewTextHandler(file, opts)
	}

	logger := slog.New(handler)

	closer := func() error {
		file.Sync()
		return file.Close()
	}

	return logger, closer, nil
}

func openOrRotate(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err == nil {
		// 5 MiB = 5 * 1024 * 1024 = 5242880 bytes
		if info.Size() >= 5242880 {
			rotatedPath := path + ".1"
			_ = os.Remove(rotatedPath) // allow rename to succeed on Windows if file exists
			_ = os.Rename(path, rotatedPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
