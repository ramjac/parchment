package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestMouseClickTracker(t *testing.T) {
	var clicks mouseClickTracker
	start := time.Now()
	if clicks.click(4, 7, start) {
		t.Fatal("first click was detected as a double click")
	}
	if !clicks.click(4, 7, start.Add(300*time.Millisecond)) {
		t.Fatal("second click was not detected")
	}
	if clicks.click(4, 7, start.Add(350*time.Millisecond)) {
		t.Fatal("third click reused the completed double click")
	}
	if clicks.click(5, 7, start.Add(400*time.Millisecond)) {
		t.Fatal("click at a different location was detected as a double click")
	}
	if clicks.click(5, 7, start.Add(time.Second)) {
		t.Fatal("slow second click was detected as a double click")
	}
}

func renderedWordPosition(lines []string, word string) (int, int) {
	for row, line := range lines {
		plain := ansi.Strip(line)
		index := strings.Index(plain, word)
		if index >= 0 {
			return row, runewidth.StringWidth(plain[:index])
		}
	}
	return -1, -1
}
