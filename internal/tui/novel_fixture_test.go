package tui

import (
	"strings"
	"testing"
)

func TestNovelDocumentFixtureOutline(t *testing.T) {
	doc := loadNovelFixture(t)
	for _, text := range []string{
		"    BOOK ONE: 1805\n",
		"    CHAPTER I\n",
		"*** END OF THE PROJECT GUTENBERG EBOOK WAR AND PEACE ***",
		"THE FULL PROJECT GUTENBERG LICENSE",
		"https://www.gutenberg.org/ebooks/2600",
	} {
		if !strings.Contains(doc.Body, text) {
			t.Fatalf("novel is missing %q", text)
		}
	}

	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.resize(120, 40)
	screen.startReading(doc)
	if screen.errMessage != "" || !screen.reading {
		t.Fatalf("novel reader failed: %s", screen.errMessage)
	}
	if len(screen.outline) != 382 || len(screen.readerRows) != 382 {
		t.Fatalf("got %d outline entries and %d rows, want 382", len(screen.outline), len(screen.readerRows))
	}
	var books, chapters int
	slugs := make(map[string]bool)
	targets := make(map[string]int)
	for i, section := range screen.outline {
		if slugs[section.Slug] {
			t.Fatalf("duplicate slug %q", section.Slug)
		}
		slugs[section.Slug] = true
		if screen.readerRows[i] < 0 || (i > 0 && screen.readerRows[i] <= screen.readerRows[i-1]) {
			t.Fatalf("heading %q has invalid reader row %d", section.Title, screen.readerRows[i])
		}
		switch section.Level {
		case 1:
			books++
			targets[section.Title] = i
		case 2:
			chapters++
		default:
			t.Fatalf("unexpected heading level %d for %q", section.Level, section.Title)
		}
	}
	if books != 17 || chapters != 365 {
		t.Fatalf("got %d books/epilogues and %d chapters", books, chapters)
	}
	firstRow := screen.reader.YOffset()
	screen.updateReaderMouse(mouseEvent{X: 3, Y: 5, Action: mousePress, Button: mouseWheelDown})
	_, scrolled := screen.outlineLines()
	if scrolled[1] != 3 || screen.reader.YOffset() != firstRow {
		t.Fatalf("novel outline scroll changed the reader or missed headings: %v", scrolled)
	}
	screen.clickOutline(3, 4)
	if screen.currentSection() != 3 || screen.reader.YOffset() != screen.readerRows[3] {
		t.Fatal("click after scrolling the novel outline did not navigate")
	}
	for _, title := range []string{"BOOK ONE: 1805", "BOOK EIGHT: 1811 - 12", "SECOND EPILOGUE"} {
		index, ok := targets[title]
		if !ok {
			t.Fatalf("missing outline entry %q", title)
		}
		screen.gotoSection(index)
		if screen.reader.YOffset() != screen.readerRows[index] || screen.currentSection() != index {
			t.Fatalf("navigation to %q landed at row %d", title, screen.reader.YOffset())
		}
		_, visible := screen.outlineLines()
		clicked := false
		for row, target := range visible {
			if target == index+1 {
				screen.clickOutline(3, row+3)
				clicked = true
				break
			}
		}
		if !clicked || screen.currentSection() != index+1 ||
			screen.reader.YOffset() != screen.readerRows[index+1] {
			t.Fatalf("outline click did not navigate to the chapter after %q", title)
		}
	}
	if screen.snapshot.Body != doc.Body {
		t.Fatal("reader changed the novel")
	}
}
