package document

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func testDocument(body string, edit func(*Layout)) Document {
	d := Document{Body: body, Layout: DefaultLayout()}
	d.Title = "Quarterly Report"
	if edit != nil {
		edit(&d.Layout)
	}
	return d
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPaginateLetterPageDimensionsAndPageNumber(t *testing.T) {
	pages, err := Paginate(testDocument("Hello world.", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d", len(pages))
	}
	lines := pages[0].Lines
	if len(lines) != 66 {
		t.Fatalf("page rows = %d, want 66", len(lines))
	}
	if want := strings.Repeat(" ", 10) + "Hello world."; lines[6] != want {
		t.Fatalf("first body line = %q, want %q", lines[6], want)
	}
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "Page 1 of 1" {
			found = true
		}
		if len(line) > 85 {
			t.Fatalf("line wider than the page: %q", line)
		}
	}
	if !found {
		t.Fatal("page number is missing")
	}
}

func TestPageAndSectionBreaksStartNewPages(t *testing.T) {
	body := "One\n\n" + PageBreakMarkup + "\n\nTwo\n\n" + SectionBreakMarkup(1, false) + "\n\nThree\n"
	pages, err := Paginate(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("pages = %d, want 3", len(pages))
	}
	for i, want := range []string{"One", "Two", "Three"} {
		if !strings.Contains(strings.Join(pages[i].Lines, "\n"), want) {
			t.Fatalf("page %d does not contain %q", i+1, want)
		}
	}
	printed, err := Print(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(printed, "\f"); got != 2 {
		t.Fatalf("form feeds = %d, want 2", got)
	}
}

func TestBreaksAtEndAndInsideCodeFencesDoNotAddPages(t *testing.T) {
	body := "Text\n\n```\n" + PageBreakMarkup + "\n```\n\n" + PageBreakMarkup + "\n"
	pages, err := Paginate(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	if !strings.Contains(strings.Join(pages[0].Lines, "\n"), "page-break") {
		t.Fatal("directive inside a code fence was not printed as code")
	}
}

func TestLongTextFlowsAcrossPages(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&body, "Paragraph number %d.\n\n", i)
	}
	pages, err := Paginate(testDocument(body.String(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 3 {
		t.Fatalf("pages = %d, want at least 3", len(pages))
	}
	for _, page := range pages {
		if len(page.Lines) != 66 {
			t.Fatalf("page %d has %d rows", page.Number, len(page.Lines))
		}
	}
	want := fmt.Sprintf("Page %d of %d", len(pages), len(pages))
	if !strings.Contains(strings.Join(pages[len(pages)-1].Lines, "\n"), want) {
		t.Fatalf("last page does not say %q", want)
	}
}

func TestHeaderFooterAndPageNumberPlacement(t *testing.T) {
	d := testDocument("Body", func(l *Layout) {
		l.Header = "{title}|Confidential"
		l.Footer = "left|middle|Page {page}/{pages}"
		l.PageNumbers = NumbersRight
	})
	pages, err := Paginate(d)
	if err != nil {
		t.Fatal(err)
	}
	lines := pages[0].Lines
	header := lines[6]
	if !strings.HasPrefix(strings.TrimSpace(header), "Quarterly Report") || !strings.HasSuffix(header, "Confidential") {
		t.Fatalf("header = %q", header)
	}
	if len(header) != 10+65 {
		t.Fatalf("right-aligned header ends at column %d, want %d", len(header), 75)
	}
	if lines[7] != "" || strings.TrimSpace(lines[8]) != "Body" {
		t.Fatalf("header separator or body misplaced: %q %q", lines[7], lines[8])
	}
	footer := ""
	for _, line := range lines {
		if strings.Contains(line, "middle") {
			footer = line
		}
	}
	if !strings.Contains(footer, "left") || !strings.HasSuffix(footer, "Page 1/1") {
		t.Fatalf("footer = %q", footer)
	}
	last := ""
	for _, line := range lines {
		if strings.HasSuffix(line, "Page 1 of 1") {
			last = line
		}
	}
	if len(last) != 75 {
		t.Fatalf("right-aligned page number line = %q", last)
	}
}

func TestColumnsFlowTopToBottomThenAcross(t *testing.T) {
	body := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu\n\n", 40)
	one, err := Paginate(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Paginate(testDocument(body, func(l *Layout) { l.Columns = 2 }))
	if err != nil {
		t.Fatal(err)
	}
	if len(two) >= len(one) {
		t.Fatalf("two columns used %d pages, one column used %d", len(two), len(one))
	}
	sideBySide := false
	for _, line := range two[0].Lines {
		if strings.Count(line, "alpha") == 2 {
			sideBySide = true
		}
	}
	if !sideBySide {
		t.Fatal("no row contains two columns of text")
	}
}

func TestContinuousSectionBalancesColumnsAndSharesPage(t *testing.T) {
	body := "Intro paragraph.\n\n" + SectionBreakMarkup(2, true) + "\n\n" +
		"first column text\n\nsecond column text\n\nthird\n\nfourth\n\n" +
		SectionBreakMarkup(1, true) + "\n\nClosing paragraph.\n"
	pages, err := Paginate(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	text := strings.Join(pages[0].Lines, "\n")
	for _, want := range []string{"Intro paragraph.", "Closing paragraph."} {
		if !strings.Contains(text, want) {
			t.Fatalf("page is missing %q", want)
		}
	}
	balanced := false
	for _, line := range pages[0].Lines {
		if strings.Contains(line, "first column text") && strings.Contains(line, "third") {
			balanced = true
		}
	}
	if !balanced {
		t.Fatalf("columns were not balanced:\n%s", text)
	}
}

func TestMarkdownIsConvertedToPrintableText(t *testing.T) {
	body := "# Title\n\nSome **bold** and *italic* text with a [link](https://example.com).\n\n" +
		"- first item that is long enough to wrap around the narrow measure of this printed page okay\n- second\n\n" +
		"![A chart](image-0123456789abcdef.png)\n\n> quoted\n\n```\ncode  line\n```\n"
	pages, err := Paginate(testDocument(body, nil))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(pages[0].Lines, "\n")
	for _, want := range []string{
		"TITLE", "=====", "Some bold and italic text with a link (https://example.com).",
		"- first item", "[Image: A chart]", "> quoted", "    code  line",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("printed text is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "**") || strings.Contains(text, "image-0123") {
		t.Fatalf("Markdown markup leaked into printed text:\n%s", text)
	}
}

func TestLayoutValidationAndGeometry(t *testing.T) {
	if err := DefaultLayout().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Layout){
		"size":        func(l *Layout) { l.PageSize = "tabloid" },
		"orientation": func(l *Layout) { l.Orientation = "sideways" },
		"columns":     func(l *Layout) { l.Columns = 9 },
		"margin":      func(l *Layout) { l.Margins.Left = -1 },
		"huge margin": func(l *Layout) { l.Margins = Margins{Top: 100, Right: 100, Bottom: 100, Left: 100} },
		"numbers":     func(l *Layout) { l.PageNumbers = "top" },
		"control":     func(l *Layout) { l.Header = "bad\x1b[31m" },
		"version":     func(l *Layout) { l.Version = 2 },
	}
	for name, edit := range cases {
		l := DefaultLayout()
		edit(&l)
		if l.Validate() == nil {
			t.Errorf("%s: invalid layout was accepted", name)
		}
	}
	a4 := DefaultLayout()
	a4.PageSize = PageA4
	g, err := a4.Geometry()
	if err != nil || g.PageRows != 70 || g.PageCols != 82 {
		t.Fatalf("a4 geometry = %+v, %v", g, err)
	}
	landscape := DefaultLayout()
	landscape.Orientation = Landscape
	g, _ = landscape.Geometry()
	if g.PageCols != 110 || g.PageRows != 51 {
		t.Fatalf("landscape geometry = %+v", g)
	}
}

func TestNewImageValidatesAndNamesByContent(t *testing.T) {
	if _, err := NewImage([]byte("not an image")); err == nil {
		t.Fatal("non-image data was accepted")
	}
	if _, err := NewImage(nil); err == nil {
		t.Fatal("empty data was accepted")
	}
	data := tinyPNG(t)
	first, err := NewImage(data)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := NewImage(data)
	if first.Name != second.Name || !IsImageName(first.Name) || !strings.HasSuffix(first.Name, ".png") {
		t.Fatalf("image names = %q, %q", first.Name, second.Name)
	}
	if got := ImageMarkdown("a [b]\nc", first.Name); got != "![a b c]("+first.Name+")" {
		t.Fatalf("markdown = %q", got)
	}
	if !ReferencedImages("x ![a](" + first.Name + ") y")[first.Name] {
		t.Fatal("embedded image was not detected")
	}
}

func TestDeeplyIndentedListStaysWithinPageWidth(t *testing.T) {
	d := testDocument(strings.Repeat(" ", 200)+"- deeply nested item text", func(l *Layout) { l.Columns = 4 })
	pages, err := Paginate(d)
	if err != nil {
		t.Fatal(err)
	}
	geometry, _ := d.Layout.Geometry()
	for _, page := range pages {
		for _, line := range page.Lines {
			if w := runewidth.StringWidth(line); w > geometry.PageCols {
				t.Fatalf("line width %d exceeds page width %d: %q", w, geometry.PageCols, line)
			}
		}
	}
	if !strings.Contains(strings.Join(pages[0].Lines, "\n"), "deeply") {
		t.Fatal("list text was lost")
	}
}

func TestImageValidateRejectsMismatchedOrCorruptData(t *testing.T) {
	img, err := NewImage(tinyPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := img.Validate(); err != nil {
		t.Fatal(err)
	}
	renamed := Image{Name: "image-0000000000000000.png", Data: img.Data}
	if renamed.Validate() == nil {
		t.Fatal("image with a name that does not match its content was accepted")
	}
	corrupt := Image{Name: img.Name, Data: []byte("not an image")}
	if corrupt.Validate() == nil {
		t.Fatal("corrupt image data was accepted")
	}
}
