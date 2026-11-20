package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/tui/termimg"
)

type documentImageRenderModel struct{ *Model }

func (m documentImageRenderModel) Init() tea.Cmd {
	size := tea.WindowSizeMsg{Width: m.width, Height: m.height}
	return func() tea.Msg { return size }
}

func TestDocumentImagesRendererUpdates(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm", "sixel"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			img := documentTestImage(t)
			h := newDocumentHarness(t, document.Draft{
				Body:   "![Diagram](" + img.Name + ")\n\n" + document.PageBreakMarkup + "\n\n" + strings.Repeat("More text.\n\n", 30) + "Text only.",
				Images: []artifact.Image{img},
			})
			h.key("esc")
			prepareGoldenView(h, 96, 30)
			s := h.m.documents
			_, row := firstDocumentImageRow(t, s, []document.Page{{Lines: s.readerLines}})
			s.reader.SetYOffset(row)
			staleSession := s.autosaveSession + 1
			out := make(terminalRenderOutput, 128)
			program := tea.NewProgram(terminalModel{Model: documentImageRenderModel{h.m}},
				tea.WithInput(nil), tea.WithOutput(keyboardOutput{out}), tea.WithoutSignals())
			done := make(chan error, 1)
			go func() {
				_, err := program.Run()
				done <- err
			}()
			defer func() {
				program.Quit()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			prefix := map[string]string{"kitty": "\x1b_Ga=T", "iterm": "\x1b]1337;File=", "sixel": "\x1bP0;1;0q"}[protocol]
			readTerminalOutputUntil(t, out, prefix)
			program.Send(mouseEvent{Action: mouseMotion, Button: mouseNone})
			program.Send(documentAutosaveTickMsg{session: staleSession})
			program.Send(tea.KeyPressMsg{Code: tea.KeyDown})
			output := readTerminalOutputUntil(t, out, prefix)
			if strings.Contains(output, "\x1b[2J") || strings.Count(output, prefix) != 1 {
				t.Fatal("scrolling cleared the terminal or transmitted separate image strips")
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyEnd})
			output = readTerminalOutputUntil(t, out, "Text only.")
			if strings.Contains(output, "\x1b[2J") || strings.Contains(output, prefix) ||
				strings.Count(output, "\n") < 29 {
				t.Fatal("scrolling past the image did not repaint its old rows without clearing")
			}
		})
	}
}

func documentTestImage(t *testing.T) artifact.Image {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			pixels.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 16), B: 100, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, pixels); err != nil {
		t.Fatal(err)
	}
	img, err := artifact.NewImage(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func documentImageEnv(t *testing.T, protocol string) {
	t.Helper()
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("LC_TERMINAL", "")
	t.Setenv("TERM", "dumb")
	switch protocol {
	case "kitty":
		t.Setenv("KITTY_WINDOW_ID", "1")
	case "iterm":
		t.Setenv("TERM_PROGRAM", "iTerm.app")
	case "sixel":
		t.Setenv("TERM", "xterm-sixel")
	}
}

func firstDocumentImageRow(t *testing.T, s *documentsScreen, pages []document.Page) (int, int) {
	t.Helper()
	for page, content := range pages {
		for row, line := range content.Lines {
			for _, r := range line {
				if _, ok := s.imageState.rows[r]; ok {
					return page, row
				}
			}
		}
	}
	t.Fatal("document has no reserved image rows")
	return 0, 0
}

func TestDocumentImagesReaderAndDraftPreview(t *testing.T) {
	documentImageEnv(t, "")
	img := documentTestImage(t)
	body := "# Illustrated\n\n![A diagram](./" + img.Name + " \"Title\")\n\n[Continue](#next)\n\n## Next\n\nAfter image."
	h := newDocumentHarness(t, document.Draft{Body: body, Images: []artifact.Image{img}})
	h.key("esc")
	s := h.m.documents
	saved := s.snapshot
	_, row := firstDocumentImageRow(t, s, []document.Page{{Lines: s.readerLines}})
	s.reader.SetYOffset(row)
	view := h.m.View().Content
	if !strings.Contains(ansi.Strip(view), "▀") {
		t.Fatalf("reader has no graphical image:\n%s", view)
	}
	if len(s.readerLinks) != 1 || s.readerLinks[0].target != "next" {
		t.Fatalf("image broke internal links: %+v", s.readerLinks)
	}
	box := s.readerLinks[0]
	if !strings.Contains(ansi.Strip(s.readerLines[box.row]), "Continue") {
		t.Fatal("link hitbox no longer points at its label")
	}
	h.key("e")
	if !strings.Contains(h.m.View().Content, img.Name) || strings.Contains(h.m.View().Content, "▀") {
		t.Fatal("source editor did not retain the canonical image link")
	}
	var draftData bytes.Buffer
	draftPixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	draftPixels.SetNRGBA(0, 0, color.NRGBA{R: 220, A: 255})
	if err := png.Encode(&draftData, draftPixels); err != nil {
		t.Fatal(err)
	}
	draftImage, err := artifact.NewImage(draftData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s.images = append(s.images, draftImage)
	s.body.SetValue("# Unsaved\n\n![Draft diagram][picture]\n\n[picture]: " + draftImage.Name + " \"Draft title\"")
	h.key("f5")
	page, row := firstDocumentImageRow(t, s, s.editPages)
	s.editPage, s.editPageLine = page, row
	if view := h.m.View().Content; !strings.Contains(ansi.Strip(view), "▀") {
		t.Fatalf("draft preview has no graphical image:\n%s", view)
	}
	if !reflect.DeepEqual(s.snapshot, saved) {
		t.Fatal("rendering changed the saved document")
	}
	stored, err := h.docs.Get(context.Background(), h.path)
	if err != nil || stored.Body != body {
		t.Fatalf("rendering changed canonical Markdown: %v", err)
	}
}

func TestDocumentImagesPaginationAndNavigation(t *testing.T) {
	documentImageEnv(t, "")
	img := documentTestImage(t)
	for _, columns := range []int{1, 2} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			h := newDocumentHarness(t, document.Draft{})
			s := h.m.documents
			layout := document.DefaultLayout()
			layout.Columns = columns
			var body strings.Builder
			body.WriteString("> ## Quoted heading\n\n")
			for i := 0; i < 20; i++ {
				fmt.Fprintf(&body, "![Diagram](%s)\n\n## Target%02d\n\n", img.Name, i)
			}
			body.WriteString("<A NAME=\"destination\"></a>\n\nAnchor destination.\n")
			doc := document.Document{Body: body.String(), Layout: layout, Images: []artifact.Image{img}}
			pages, _, err := s.paginateDocumentWithLinks(doc)
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) < 2 {
				t.Fatal("images did not reserve space across pages")
			}
			imageCells := 0
			for p, page := range pages {
				for _, line := range page.Lines {
					for _, r := range line {
						if _, ok := s.imageState.rows[r]; ok {
							imageCells++
						}
						if _, ok := s.imageState.navigation[r]; ok {
							t.Fatal("navigation marker leaked into page")
						}
					}
					for _, section := range s.imageState.outline {
						if !section.Anchor && strings.Contains(ansi.Strip(line), section.Title) && section.Page != p+1 {
							t.Fatalf("%s points to page %d, displayed on %d", section.Title, section.Page, p+1)
						}
					}
				}
				rendered := s.renderDocumentImages(strings.Join(page.Lines, "\n"), 120)
				if strings.Count(rendered, "\n") != len(page.Lines)-1 {
					t.Fatal("rendering changed page height")
				}
			}
			expectedCells := 0
			for _, row := range s.imageState.rows {
				expectedCells += row.width
			}
			if imageCells != 20*expectedCells {
				t.Fatalf("page/column boundary lost image cells: %d", imageCells)
			}
			s.outline = s.imageState.outline
			s.editPages = pages
			s.navigateToTargetPreview("target19")
			if s.editPage != s.outline[19].Page-1 || s.editPage == 0 {
				t.Fatal("preview link did not navigate to the image-expanded page")
			}
			anchor := s.outline[len(s.outline)-1]
			if !anchor.Anchor || anchor.Page != len(pages) {
				t.Fatalf("anchor was not relocated after images: %+v", anchor)
			}
		})
	}
}

func TestDocumentImagesUnavailable(t *testing.T) {
	documentImageEnv(t, "")
	img := documentTestImage(t)
	for _, tt := range []struct {
		name string
		data []byte
		want string
	}{
		{"missing", nil, "[missing image: Diagram]"},
		{"corrupt", []byte("not an image"), "[unavailable image: Diagram]"},
		{"oversized", []byte("GIF89a\xff\xff\xff\xff\x00\x00\x00"), "[unavailable image: Diagram]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newDocumentHarness(t, document.Draft{})
			doc := document.Document{Body: "![Diagram](" + img.Name + ")", Layout: document.DefaultLayout()}
			if tt.data != nil {
				broken := img
				broken.Data = tt.data
				doc.Images = []artifact.Image{broken}
			}
			pages, _, err := h.m.documents.paginateDocumentWithLinks(doc)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ansi.Strip(strings.Join(pages[0].Lines, "\n")), tt.want) {
				t.Fatalf("unavailable image did not retain a readable label: %+v", pages[0].Lines)
			}
			if len(h.m.documents.imageState.rows) != 0 {
				t.Fatal("unavailable image reserved graphical rows")
			}
		})
	}
}

func TestDocumentImagesProtocolsAndTransitions(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm", "sixel"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			img := documentTestImage(t)
			h := newDocumentHarness(t, document.Draft{
				Body:   "# Image\n\n![Diagram](" + img.Name + ")\n\n" + document.PageBreakMarkup + "\n\n" + strings.Repeat("More text.\n\n", 30) + "Text only.",
				Images: []artifact.Image{img},
			})
			h.key("esc")
			s := h.m.documents
			_, row := firstDocumentImageRow(t, s, []document.Page{{Lines: s.readerLines}})
			s.reader.SetYOffset(row)
			view := h.m.View().Content
			prefix := map[string]string{"kitty": "\x1b_Ga=T", "iterm": "\x1b]1337;File=", "sixel": "\x1bP0;1;0q"}[protocol]
			if !strings.Contains(view, prefix) || !strings.Contains(view, "\x1b[s") || !strings.Contains(view, "\x1b[u") {
				t.Fatal("reader did not preserve the cursor around graphical output")
			}
			if strings.Count(view, prefix) != 1 {
				t.Fatal("reader split a contiguous image into terminal-row strips")
			}
			textFrame, placements := s.documentImagePlacements(s.documentView("header"), s.width)
			if len(placements) != 1 || placements[0].height != 8 || placements[0].sourceRow != 0 {
				t.Fatalf("image was not coalesced into one visible rectangle: %+v", placements)
			}
			placement := placements[0]
			rendered := strings.Split(s.renderDocumentImages(s.documentView("header"), s.width), "\n")
			last := placement.y + placement.height - 1
			if !strings.HasPrefix(rendered[last], strings.Split(textFrame, "\n")[last]+"\x1b[s") ||
				!strings.Contains(rendered[last], prefix) || !strings.HasSuffix(rendered[last], "\x1b[u") {
				t.Fatal("image was not painted after its complete text rectangle")
			}
			frame := s.imageState.frame
			for _, message := range []tea.Msg{
				tea.KeyPressMsg{Code: []rune(string("?"))[0], Text: string("?")},
				mouseEvent{Action: mouseMotion, Button: mouseNone},
				documentAutosaveTickMsg{session: s.autosaveSession + 1},
			} {
				_, cmd := h.m.Update(message)
				if cmd != nil || frame != s.imageState.frame || h.m.View().Content != view {
					t.Fatal("unrelated input or background update repainted the image")
				}
			}
			_, cmd := h.m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			if cmd != nil || s.imageState.frame == frame {
				t.Fatal("scrolling must invalidate the frame without clearing the screen")
			}
			_, placements = s.documentImagePlacements(s.documentView("header"), s.width)
			if len(placements) != 1 || placements[0].sourceRow != 1 || placements[0].height != 7 {
				t.Fatalf("partial scrolling lost the contiguous source crop: %+v", placements)
			}
			if strings.Count(ansi.Strip(view), "\n")+1 != h.m.height {
				t.Fatal("graphical output changed the terminal frame height")
			}
			prepareGoldenView(h, 60, 16)
			if view := h.m.View().Content; tuiScreenWidth(view) > 60 || strings.Count(ansi.Strip(view), "\n")+1 != 16 {
				t.Fatal("resizing an image overflowed the terminal")
			}
			prepareGoldenView(h, 30, 8)
			view = h.m.View().Content
			if !strings.Contains(view, "terminal too small") || strings.Contains(view, prefix) {
				t.Fatal("undersized terminal retained graphical output")
			}
			if protocol == "kitty" && !strings.HasPrefix(view, termimg.ClearKittyPlacements()) {
				t.Fatal("undersized terminal did not clear Kitty placements")
			}
			prepareGoldenView(h, 96, 30)
			h.key("end")
			if strings.Contains(h.m.View().Content, prefix) {
				t.Fatal("scrolling to text retained image graphics")
			}
			h.key("home")
			_, cmd = h.m.Update(tea.KeyPressMsg{Code: []rune(string("e"))[0], Text: string("e")})
			h.run(cmd)
			view = h.m.View().Content
			if strings.Contains(view, prefix) {
				t.Fatal("editor retained reader image graphics")
			}
			if protocol == "kitty" && !strings.HasPrefix(view, termimg.ClearKittyPlacements()) {
				t.Fatal("editor transition did not clear Kitty placements")
			}
			h.key("f5")
			page, row := firstDocumentImageRow(t, s, s.editPages)
			s.editPage, s.editPageLine = page, row
			if !strings.Contains(h.m.View().Content, prefix) {
				t.Fatal("print preview omitted image graphics")
			}
			h.key("esc")
			if strings.Contains(h.m.View().Content, prefix) {
				t.Fatal("leaving print preview retained image graphics")
			}
		})
	}
}

func TestDocumentImagesMarkdownAndClipping(t *testing.T) {
	documentImageEnv(t, "")
	img := documentTestImage(t)
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	body := "# Images\n\nBefore ![Inline](" + img.Name + ") between ![Reference][ref] after.\n\n````markdown\n![Example](" + img.Name + ")\n````\n\n![Remote](https://example.invalid/picture.png)\n\n" + document.PageBreakMarkup + "\n\n[ref]: " + img.Name
	doc := document.Document{Body: body, Layout: document.DefaultLayout(), Images: []artifact.Image{img}}
	pages, _, err := s.paginateDocumentWithLinks(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.imageState.prepared) != 1 {
		t.Fatal("repeated image was not decoded only once")
	}
	var all strings.Builder
	for _, page := range pages {
		all.WriteString(strings.Join(page.Lines, "\n"))
	}
	plain := ansi.Strip(all.String())
	if !strings.Contains(plain, "[Inline]") || !strings.Contains(plain, "[Reference]") ||
		!strings.Contains(plain, "![Example]") || !strings.Contains(plain, "Remote") {
		t.Fatalf("Markdown image parsing changed code or labels:\n%s", plain)
	}
	last := -1
	for _, text := range []string{"Before", "[Inline]", "between", "[Reference]", "after."} {
		index := strings.Index(plain, text)
		if index <= last {
			t.Fatalf("inline images lost surrounding prose or order: %s", plain)
		}
		last = index
	}
	page, row := firstDocumentImageRow(t, s, pages)
	for _, width := range []int{1, 8, 24, 80} {
		content := pageText(document.Page{Lines: pages[page].Lines[row : row+2]}, width)
		rendered := s.renderDocumentImages(content, width)
		if strings.Count(rendered, "\n") != 1 || ansi.StringWidth(strings.Split(rendered, "\n")[0]) > width {
			t.Fatalf("image overflow at width %d: %q", width, rendered)
		}
		for _, r := range rendered {
			if _, ok := s.imageState.rows[r]; ok {
				t.Fatal("internal image marker leaked into rendered content")
			}
		}
	}
}
