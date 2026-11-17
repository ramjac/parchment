package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupDiscard(t *testing.T) {
	dir := t.TempDir()
	logger, closer, err := Setup(dir, "off", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer closer()

	logger.Info("this should be discarded")
	logPath := filepath.Join(dir, "parchment.log")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("log file should not have been created")
	}
}

func TestSetupLevelAndFormat(t *testing.T) {
	dir := t.TempDir()
	logger, closer, err := Setup(dir, "debug", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Debug("debug message")
	logger.Info("info message")

	err = closer()
	if err != nil {
		t.Fatalf("unexpected error closing log file: %v", err)
	}

	logPath := filepath.Join(dir, "parchment.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("unexpected error reading log file: %v", err)
	}

	if !strings.Contains(string(content), `"debug message"`) {
		t.Fatalf("expected debug message in JSON log, got %s", string(content))
	}
}

func TestSetupRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "parchment.log")

	// Create a dummy log file larger than 5 MiB
	largeContent := bytes.Repeat([]byte("a"), 5*1024*1024+1)
	err := os.WriteFile(logPath, largeContent, 0o600)
	if err != nil {
		t.Fatalf("unexpected error creating large log file: %v", err)
	}

	logger, closer, err := Setup(dir, "info", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logger.Info("new message")
	err = closer()
	if err != nil {
		t.Fatalf("unexpected error closing log file: %v", err)
	}

	// Verify rotation
	rotatedPath := logPath + ".1"
	if _, err := os.Stat(rotatedPath); os.IsNotExist(err) {
		t.Fatalf("rotated log file was not created")
	}

	newContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("unexpected error reading new log file: %v", err)
	}

	if !strings.Contains(string(newContent), "new message") {
		t.Fatalf("expected new message in rotated log, got %s", string(newContent))
	}
}
