package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/config"
	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/imageedit"
	tuitheme "github.com/ramjac/parchment/internal/tui/theme"
)

type imageHarness struct {
	t    *testing.T
	m    *imageModel
	path string
	quit bool
}

func newImageHarness(t *testing.T) *imageHarness {
	t.Helper()
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "picture.png")
	pixels := image.NewNRGBA(image.Rect(0, 0, 24, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 24; x++ {
			pixels.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 10), G: uint8(y * 15), B: 100, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, pixels); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	service := imageedit.NewService(repo)
	root := NewModel(Config{
		Path: path, Kind: artifact.ImageKind, Images: service, Recovery: repo,
		Theme: config.Theme{Mode: "dark"}, UndoLimit: 5,
	})
	_, _ = root.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_, cmd := root.Update(root.Init()())
	if root.stage != stageImage || root.image == nil || root.opened.image != nil {
		t.Fatal("root failed to open image stage", root.errMessage)
	}
	h := &imageHarness{t: t, m: root.image, path: path}
	h.run(cmd)
	return h
}

func (h *imageHarness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			h.run(sub)
		}
	case tea.QuitMsg:
		h.quit = true
	case cursor.BlinkMsg:
		// Form focus schedules one blink; avoid subsequent timer commands.
	case nil:
	default:
		_, next := h.m.Update(msg)
		h.run(next)
	}
}

func (h *imageHarness) send(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	for i := range h.m.fields {
		setTextInputCursorStatic(&h.m.fields[i])
	}
	h.run(cmd)
}

func (h *imageHarness) key(key string) { h.send(sheetKey(key)) }

func (h *imageHarness) form(key string, values ...string) {
	h.t.Helper()
	h.key(key)
	if len(h.m.fields) != len(values) {
		h.t.Fatalf("form %s fields=%d, want %d", key, len(h.m.fields), len(values))
	}
	for i, value := range values {
		h.m.fields[i].SetValue(value)
	}
	h.key("enter")
}

func TestImageTUIEditsAndSaves(t *testing.T) {
	documentImageEnv(t, "")
	h := newImageHarness(t)
	original, _ := os.ReadFile(h.path)
	h.form("c", "4", "2", "12", "8")
	if h.m.session.Image().Bounds().Size() != image.Pt(12, 8) || !h.m.session.Dirty() {
		t.Fatal("crop did not edit image")
	}
	h.key("ctrl+s")
	if h.m.prompt != imageConfirmSave {
		t.Fatal("in-place save did not require confirmation")
	}
	h.key("enter")
	if got, _ := os.ReadFile(h.path); !bytes.Equal(got, original) {
		t.Fatal("default confirmation changed original")
	}
	h.key("ctrl+s")
	h.key("y")
	if h.m.session.Dirty() || h.m.err != "" {
		t.Fatal("confirmed save failed", h.m.err)
	}
	h.form("r", "6", "")
	if h.m.session.Image().Bounds().Size() != image.Pt(6, 4) {
		t.Fatal("proportional resize failed")
	}
	h.key("ctrl+s")
	h.key("y")
	if h.m.session.Dirty() || h.m.err != "" {
		t.Fatal("repeat in-place save failed", h.m.err)
	}
	h.key("u")
	if !h.m.session.Dirty() || h.m.session.Image().Bounds().Size() != image.Pt(12, 8) {
		t.Fatal("save discarded history")
	}
	h.key("ctrl+r")
	if h.m.session.Dirty() {
		t.Fatal("redo to saved revision must be clean")
	}
	dest := filepath.Join(filepath.Dir(h.path), "copy.jpg")
	h.form("s", dest)
	if h.m.session.SavingImage().Path() != dest || h.m.session.SavingImage().Format() != "jpeg" || h.m.err != "" {
		t.Fatal("save-as failed to switch active image", h.m.err)
	}
	h.key("u")
	h.key("ctrl+s")
	h.key("y")
	data, _ := os.ReadFile(dest)
	saved, err := imageedit.Decode(data)
	if err != nil || saved.Format() != "jpeg" || saved.Bounds().Size() != image.Pt(12, 8) {
		t.Fatal("undo and save-as rebase failed", err)
	}
	h.key("q")
	if !h.quit {
		t.Fatal("clean image did not quit")
	}
}

func TestImageTUIFormsConflictsAndDirtyQuit(t *testing.T) {
	documentImageEnv(t, "")
	h := newImageHarness(t)
	h.form("r", "", "")
	if h.m.err == "" || h.m.session.Dirty() || h.m.prompt != imageResize {
		t.Fatal("invalid resize changed state or closed form")
	}
	h.m.fields[0].SetValue("0")
	h.key("enter")
	if h.m.err == "" {
		t.Fatal("explicit zero accepted")
	}
	h.key("esc")
	h.form("c", "24", "0", "1", "1")
	if h.m.err == "" || h.m.session.CanUndo() {
		t.Fatal("out-of-bounds crop changed history")
	}
	h.key("esc")
	h.form("r", "12", "")
	h.key("s")
	h.send(tea.KeyPressMsg{Code: []rune(string("curious.png"))[0], Text: string("curious.png")})
	if h.m.prompt != imageSaveAs || h.m.fields[0].Value() != "curious.png" || h.quit {
		t.Fatal("path input triggered browse shortcuts")
	}
	h.key("esc")
	h.form("s", h.path)
	if h.m.err == "" {
		t.Fatal("save-as source alias accepted")
	}
	h.key("esc")
	dest := filepath.Join(filepath.Dir(h.path), "existing.png")
	if err := os.WriteFile(dest, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.form("s", dest)
	if h.m.prompt != imageConfirmSave {
		t.Fatal("existing destination did not require confirmation")
	}
	if err := os.WriteFile(dest, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.key("y")
	if h.m.err == "" || !h.m.session.Dirty() || h.m.session.SavingImage().Path() != h.path {
		t.Fatal("save conflict lost unsaved work/active file")
	}
	if got, _ := os.ReadFile(dest); string(got) != "external" {
		t.Fatal("confirmed stale destination was overwritten")
	}
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		h.key(key)
		if h.quit || h.m.prompt != imageConfirmQuit {
			t.Fatalf("%s bypassed dirty quit confirmation", key)
		}
		h.key("enter")
	}
	h.send(tea.WindowSizeMsg{Width: 25, Height: 6})
	h.key("q")
	if !strings.Contains(h.m.View().Content, "Discard edits?") {
		t.Fatal("small terminal hid dirty quit guard")
	}
	h.key("n")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !h.m.session.Dirty() {
		t.Fatal("small terminal lost edits")
	}
	h.key("q")
	h.key("y")
	if !h.quit {
		t.Fatal("confirmed dirty quit failed")
	}
}

func TestImageTUIPendingAndStaleResults(t *testing.T) {
	documentImageEnv(t, "")
	h := newImageHarness(t)
	h.key("r")
	h.m.fields[0].SetValue("12")
	_, cmd := h.m.Update(sheetKey("enter"))
	h.key("q")
	if h.quit || !h.m.pending {
		t.Fatal("pending edit did not gate quitting")
	}
	h.key("ctrl+c")
	h.run(cmd)
	if h.m.session.Dirty() || h.m.pending || h.m.err == "" {
		t.Fatal("cancelled edit changed pixels or hung")
	}
	before := h.m.View().Content
	h.send(imageEditedMsg{seq: h.m.seq - 1, img: h.m.session.Image()})
	h.send(imagePreviewMsg{seq: h.m.renderSeq - 1, output: "STALE", rows: 1})
	if h.m.View().Content != before {
		t.Fatal("stale operation/render message changed current frame")
	}
}

func TestImageTUISmallTerminalPreservesForm(t *testing.T) {
	documentImageEnv(t, "")
	h := newImageHarness(t)
	h.key("r")
	h.m.fields[0].SetValue("7")
	h.key("tab")
	h.send(tea.WindowSizeMsg{Width: 25, Height: 6})
	h.send(tea.KeyPressMsg{Code: []rune(string("hidden"))[0], Text: string("hidden")})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	if h.m.prompt != imageResize || h.m.focus != 1 || h.m.fields[0].Value() != "7" || h.m.fields[1].Value() != "" {
		t.Fatal("terminal resize lost form state or accepted invisible input")
	}
	h.key("enter")
	if h.m.session.Image().Bounds().Dx() != 7 {
		t.Fatal("restored form could not be submitted")
	}
}

func TestImageTUIGoldens(t *testing.T) {
	old := terminalColorProfile
	terminalColorProfile = colorprofile.TrueColor
	t.Cleanup(func() { terminalColorProfile = old })
	documentImageEnv(t, "")
	h := newImageHarness(t)
	normalized := func() string {
		lines := strings.Split(h.m.View().Content, "\n")
		lines[0] = h.m.theme.Chrome.Render("parchment image  fixtures/picture.png")
		const sizeLabel = "saved file: "
		if start := strings.Index(lines[3], sizeLabel); start >= 0 {
			start += len(sizeLabel)
			if end := strings.Index(lines[3][start:], " bytes"); end >= 0 {
				lines[3] = lines[3][:start] + strings.Repeat("X", end) + lines[3][start+end:]
			}
		}
		return strings.Join(lines, "\n")
	}
	assertTUIViewGolden(t, "image-viewer", normalized(), 80, 24)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	h.key("c")
	setTextInputCursorStatic(&h.m.fields[h.m.focus])
	if strings.Count(normalized(), "\x1b[7;") != 1 {
		t.Fatal("crop form must show exactly one focused cursor")
	}
	assertTUIViewGolden(t, "image-crop-small", normalized(), 40, 10)
}

func TestImageTUIMonochromeFallback(t *testing.T) {
	old := terminalColorProfile
	terminalColorProfile = colorprofile.Ascii
	t.Cleanup(func() { terminalColorProfile = old })
	documentImageEnv(t, "")
	h := newImageHarness(t)
	view := h.m.View().Content
	if !strings.Contains(view, "▀") || !strings.Contains(view, "24x16") || strings.Contains(view, "\x1b_G") {
		t.Fatal("monochrome terminal lost image fallback or metadata")
	}
}

func TestImageTUIBoundedViews(t *testing.T) {
	old := terminalColorProfile
	terminalColorProfile = colorprofile.TrueColor
	t.Cleanup(func() { terminalColorProfile = old })
	for _, mode := range []string{"dark", "light"} {
		for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 10}, {25, 6}} {
			t.Run(fmt.Sprintf("%s-%dx%d", mode, size[0], size[1]), func(t *testing.T) {
				documentImageEnv(t, "")
				h := newImageHarness(t)
				h.m.theme = tuitheme.Resolve(config.Theme{Mode: mode})
				h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				check := func() {
					t.Helper()
					view := h.m.View().Content
					if h.m.View().Content != view {
						t.Fatal("View mutated state")
					}
					lines := strings.Split(ansi.Strip(view), "\n")
					if len(lines) > size[1] {
						t.Fatalf("height %d > %d:\n%s", len(lines), size[1], view)
					}
					for _, line := range lines {
						if ansi.StringWidth(line) > size[0] {
							t.Fatalf("row width %d > %d", ansi.StringWidth(line), size[0])
						}
					}
					if size[0] >= 40 && (!strings.Contains(view, "\x1b[") ||
						!strings.Contains(ansi.Strip(view), "24x16")) {
						t.Fatal("missing semantic styles or metadata")
					}
				}
				check()
				if size[0] >= 40 {
					h.key("c")
					check()
					h.key("tab")
					h.key("shift+tab")
					if h.m.focus != 0 {
						t.Fatal("form focus traversal failed")
					}
					h.key("esc")
					check()
				}
			})
		}
	}
}

type imageRenderModel struct{ *imageModel }

func (m imageRenderModel) Init() tea.Cmd { return nil }
func (m imageRenderModel) View() tea.View {
	view := m.imageModel.View()
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func TestImageTUIProtocolRendererUpdates(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm", "sixel"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			h := newImageHarness(t)
			m := h.m
			prefix := map[string]string{"kitty": "\x1b_Ga=T", "iterm": "\x1b]1337;File=", "sixel": "\x1bP0;1;0q"}[protocol]
			view := m.View().Content
			paintAt := strings.Index(view, prefix)
			if paintAt < 0 || strings.Contains(view, "\x1b[0A") {
				t.Fatal("missing graphical preview or invalid cursor move")
			}
			paintRow := strings.Count(view[:paintAt], "\n")
			if paintRow != 4+m.previewRows-1 {
				t.Fatal("image painted before its reserved rows")
			}
			out := make(terminalRenderOutput, 128)
			program := tea.NewProgram(terminalModel{Model: imageRenderModel{m}}, tea.WithInput(nil),
				tea.WithOutput(keyboardOutput{out}), tea.WithWindowSize(m.width, m.height),
				tea.WithoutSignals())
			done := make(chan error, 1)
			go func() { _, err := program.Run(); done <- err }()
			defer func() {
				program.Quit()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			_ = readTerminalOutputUntil(t, out, prefix)
			program.Send(sheetKey("ctrl+s"))
			output := readTerminalOutputUntil(t, out, "No unsaved changes")
			if strings.Contains(output, prefix) || strings.Contains(output, "\x1b[2J") {
				t.Fatal("status update retransmitted image or cleared screen")
			}
			program.Send(tea.WindowSizeMsg{Width: 60, Height: 18})
			output = readTerminalOutputUntil(t, out, prefix)
			if strings.Count(output, prefix) != 1 {
				t.Fatalf("resize flickered or duplicated image (%d prefixes): %q",
					strings.Count(output, prefix), output[:min(len(output), 1000)])
			}
			program.Send(tea.WindowSizeMsg{Width: 25, Height: 6})
			output = readTerminalOutputUntil(t, out, "Terminal too small")
			if strings.Contains(output, prefix) {
				t.Fatal("small terminal kept image graphics")
			}
		})
	}
}

func TestImageTUISingleRowGraphics(t *testing.T) {
	documentImageEnv(t, "kitty")
	h := newImageHarness(t)
	h.form("r", "120", "1")
	if h.m.previewRows != 1 || !h.m.previewGraphics || strings.Contains(h.m.View().Content, "\x1b[0A") {
		t.Fatal("single-row image moved the cursor above its reserved rectangle")
	}
}

func TestImageTUIRejectsMalformedLoad(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "broken.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModel(Config{Path: path, Kind: artifact.ImageKind, Images: imageedit.NewService(repo)})
	_, _ = m.Update(m.Init()())
	if m.stage != stageFailed || m.image != nil || m.errMessage == "" {
		t.Fatal("malformed image reached usable editor")
	}
}
