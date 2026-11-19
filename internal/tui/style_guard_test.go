package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var hardcodedUIColor = regexp.MustCompile(`#([[:xdigit:]]{3,8})\b|lipgloss\.Color\s*\(\s*["']|lipgloss\.AdaptiveColor\s*\{`)

func TestUIColorsUseSemanticTheme(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch filepath.Base(path) {
			case "theme", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for lineNumber, line := range strings.Split(string(source), "\n") {
			if hardcodedUIColor.MatchString(line) {
				t.Errorf("%s:%d: use a semantic theme role instead of a hard-coded UI color",
					path, lineNumber+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
