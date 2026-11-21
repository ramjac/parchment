package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/imageedit"
	"example.com/parchment/internal/tui/termimg"
)

type imagePrompt int

const (
	imageBrowse imagePrompt = iota
	imageCrop
	imageResize
	imageSaveAs
	imageConfirmSave
	imageConfirmQuit
)

type imageEditedMsg struct {
	seq uint64
	img *imageedit.Image
	err error
}

type imagePreparedMsg struct {
	seq     uint64
	img     *imageedit.Image
	target  *imageedit.SaveTarget
	confirm bool
	err     error
}

type imageSavedMsg struct {
	seq uint64
	img *imageedit.Image
	err error
}

type imagePreviewMsg struct {
	seq      uint64
	output   string
	rows     int
	graphics bool
}

type imageModel struct {
	service  *imageedit.Service
	session  *imageedit.Session
	theme    Theme
	width    int
	height   int
	prompt   imagePrompt
	fields   []textinput.Model
	focus    int
	status   string
	err      string
	pending  bool
	seq      uint64
	quitting bool

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
	cancelPreview       context.CancelFunc
	renderSeq           uint64
	frame               uint64
	protocol            termimg.GraphicsProtocol
	graphicsUsed        bool
	preview             string
	previewRows         int
	previewGraphics     bool
	prepared            imagePreparedMsg
}

func newImageModel(service *imageedit.Service, img *imageedit.Image, theme Theme, undoLimit int) *imageModel {
	return &imageModel{
		service: service, session: imageedit.NewSession(img, undoLimit), theme: theme,
		protocol: termimg.DetectProtocol(),
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	}
}

func (m *imageModel) Init() tea.Cmd { return m.renderPreview() }

func (m *imageModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		if m.width == msg.Width && m.height == msg.Height {
			return m, nil
		}
		m.width, m.height = msg.Width, msg.Height
		for i := range m.fields {
			m.fields[i].SetWidth(max(1, m.width-18))
		}
		return m, m.renderPreview()
	case imagePreviewMsg:
		if msg.seq == m.renderSeq {
			m.preview, m.previewRows = msg.output, msg.rows
			m.previewGraphics = msg.graphics
			m.frame++
			if msg.graphics {
				m.graphicsUsed = true
			}
		}
		return m, nil
	case imageEditedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		changed := m.session.Apply(msg.img)
		m.status = "Image unchanged"
		if changed {
			m.status = "Edited image"
			if !m.session.CanUndo() {
				m.status += "; undo unavailable (history memory limit)"
			}
		}
		return m, m.closePrompt()
	case imagePreparedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.prepared = msg
		if msg.confirm {
			m.prompt, m.fields = imageConfirmSave, nil
			return m, m.renderPreview()
		}
		return m, m.save()
	case imageSavedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.err = msg.err.Error()
			// A new attempt must prepare a fresh target and ask again.
			m.prepared = imagePreparedMsg{}
			if m.prompt == imageConfirmSave {
				return m, m.closePrompt()
			}
			return m, nil
		}
		m.session.Saved(msg.img)
		m.status = "Saved " + msg.img.Path()
		m.prepared = imagePreparedMsg{}
		return m, m.closePrompt()
	case tea.KeyPressMsg:
		return m, m.updateKey(msg)
	}
	if len(m.fields) > 0 && !m.pending {
		var cmd tea.Cmd
		m.fields[m.focus], cmd = m.fields[m.focus].Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *imageModel) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.pending {
		if key == "ctrl+c" && m.cancelOperation != nil {
			m.cancelOperation()
			m.status = "Cancelling operation"
		}
		return nil
	}
	if (m.width < 40 || m.height < 10) && m.prompt != imageBrowse && m.prompt != imageConfirmQuit {
		if key == "esc" || key == "ctrl+c" {
			m.prepared = imagePreparedMsg{}
			return m.closePrompt()
		}
		return nil
	}
	if m.prompt == imageConfirmSave || m.prompt == imageConfirmQuit {
		switch key {
		case "y", "Y":
			if m.prompt == imageConfirmQuit {
				return m.quit()
			}
			return m.save()
		case "n", "N", "enter", "esc", "ctrl+c", "q":
			m.prepared = imagePreparedMsg{}
			return m.closePrompt()
		}
		return nil
	}
	if len(m.fields) > 0 {
		switch key {
		case "esc", "ctrl+c":
			m.err = ""
			return m.closePrompt()
		case "tab", "shift+tab":
			m.fields[m.focus].Blur()
			step := 1
			if key == "shift+tab" {
				step = len(m.fields) - 1
			}
			m.focus = (m.focus + step) % len(m.fields)
			return m.fields[m.focus].Focus()
		case "enter":
			return m.submit()
		default:
			var cmd tea.Cmd
			m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
			return cmd
		}
	}
	if key == "q" || key == "esc" || key == "ctrl+c" {
		if m.session.Dirty() {
			m.prompt = imageConfirmQuit
			return m.renderPreview()
		}
		return m.quit()
	}
	if m.width < 40 || m.height < 10 {
		return nil
	}
	m.err = ""
	switch key {
	case "c":
		b := m.session.Image().Bounds()
		return m.openForm(imageCrop, "0", "0", strconv.Itoa(b.Dx()), strconv.Itoa(b.Dy()))
	case "r":
		return m.openForm(imageResize, "", "")
	case "s":
		return m.openForm(imageSaveAs, "")
	case "ctrl+s":
		if !m.session.Dirty() {
			m.status = "No unsaved changes"
			return nil
		}
		return m.prepareSave("", true)
	case "u", "ctrl+r":
		var err error
		if key == "u" {
			err = m.session.Undo()
			m.status = "Undid image edit"
		} else {
			err = m.session.Redo()
			m.status = "Redid image edit"
		}
		if err != nil {
			m.err = err.Error()
			return nil
		}
		return m.renderPreview()
	}
	return nil
}

func (m *imageModel) openForm(prompt imagePrompt, values ...string) tea.Cmd {
	m.prompt, m.focus, m.err = prompt, 0, ""
	m.fields = make([]textinput.Model, len(values))
	for i, value := range values {
		field := textinput.New()
		field.Prompt = ""
		field.CharLimit = 12
		if prompt == imageSaveAs {
			field.CharLimit = 64 << 10
			field.Placeholder = "output.png or output.jpg"
		}
		field.SetWidth(max(1, m.width-18))
		setTextInputStyles(&field, m.theme.Chrome, m.theme.Image)
		styles := field.Styles()
		styles.Cursor.Color = m.theme.Accent.GetForeground()
		field.SetStyles(styles)
		field.Blur()
		field.SetValue(value)
		m.fields[i] = field
	}
	return tea.Batch(m.fields[0].Focus(), m.renderPreview())
}

func (m *imageModel) closePrompt() tea.Cmd {
	m.prompt, m.fields, m.focus = imageBrowse, nil, 0
	return m.renderPreview()
}

func (m *imageModel) submit() tea.Cmd {
	m.err = ""
	if m.prompt == imageSaveAs {
		path := m.fields[0].Value()
		if strings.TrimSpace(path) == "" {
			m.err = "An output path is required"
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			m.err = err.Error()
			return nil
		}
		return m.prepareSave(abs, false)
	}
	values := make([]int, len(m.fields))
	for i, field := range m.fields {
		text := strings.TrimSpace(field.Value())
		if text == "" && m.prompt == imageResize {
			continue
		}
		value, err := strconv.Atoi(text)
		if err != nil || value < 0 || (value == 0 && (m.prompt == imageResize || i >= 2)) {
			m.err = "Use positive pixel dimensions and nonnegative crop coordinates"
			return nil
		}
		values[i] = value
	}
	if m.prompt == imageResize && values[0] == 0 && values[1] == 0 {
		m.err = "Enter width or height; leave the other blank to preserve proportions"
		return nil
	}
	img, prompt := m.session.Image(), m.prompt
	ctx, seq := m.startOperation()
	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return imageEditedMsg{seq: seq, err: err}
		}
		var result *imageedit.Image
		var err error
		if prompt == imageCrop {
			result, err = img.Crop(values[0], values[1], values[2], values[3])
		} else {
			result, err = img.Resize(values[0], values[1])
		}
		if err == nil {
			err = ctx.Err()
		}
		return imageEditedMsg{seq: seq, img: result, err: err}
	}
}

func (m *imageModel) startOperation() (context.Context, uint64) {
	if m.cancelOperation != nil {
		m.cancelOperation()
	}
	ctx, cancel := m.newOperationContext()
	m.cancelOperation, m.pending = cancel, true
	m.seq++
	return ctx, m.seq
}

func (m *imageModel) finishOperation() {
	if m.cancelOperation != nil {
		m.cancelOperation()
		m.cancelOperation = nil
	}
	m.pending = false
}

func (m *imageModel) prepareSave(path string, inPlace bool) tea.Cmd {
	img, service := m.session.SavingImage(), m.service
	ctx, seq := m.startOperation()
	return func() tea.Msg {
		target, err := service.PrepareSave(ctx, img, path, inPlace, false)
		confirm := inPlace
		if !inPlace && errors.Is(err, fs.ErrExist) {
			confirm = true
			target, err = service.PrepareSave(ctx, img, path, false, true)
		}
		return imagePreparedMsg{seq: seq, img: img, target: target, confirm: confirm, err: err}
	}
}

func (m *imageModel) save() tea.Cmd {
	prepared, service := m.prepared, m.service
	ctx, seq := m.startOperation()
	return func() tea.Msg {
		img, err := service.SaveRebased(ctx, prepared.img, prepared.target)
		return imageSavedMsg{seq: seq, img: img, err: err}
	}
}

func (m *imageModel) quit() tea.Cmd {
	m.quitting = true
	if m.cancelPreview != nil {
		m.cancelPreview()
	}
	m.frame++
	return tea.Quit
}

func (m *imageModel) formLines() []string {
	switch m.prompt {
	case imageCrop:
		lines := []string{"Crop (oriented pixels): Tab fields; Enter apply; Esc cancel",
			"X: " + m.fields[0].View(), "Y: " + m.fields[1].View(),
			"Width: " + m.fields[2].View(), "Height: " + m.fields[3].View()}
		if m.height == 10 {
			lines = lines[1:]
		}
		return lines
	case imageResize:
		return []string{"Resize: blank dimension keeps ratio; both set exact size",
			"Width: " + m.fields[0].View(), "Height: " + m.fields[1].View(),
			"Tab fields; Enter apply; Esc cancel"}
	case imageSaveAs:
		return []string{"Save as: .png / .jpg / .jpeg; Enter save; Esc cancel", m.fields[0].View()}
	case imageConfirmSave:
		return []string{"Replace image? [y/N]  Esc cancels", sanitizeTerminalLine(m.prepared.target.Path())}
	case imageConfirmQuit:
		return []string{"Discard unsaved image edits and quit? [y/N]"}
	}
	return nil
}

func (m *imageModel) previewSize() (int, int) {
	if m.width < 40 || m.height < 10 || m.quitting {
		return 0, 0
	}
	return max(0, m.width-4), max(0, m.height-6-len(m.formLines()))
}

func (m *imageModel) renderPreview() tea.Cmd {
	if m.cancelPreview != nil {
		m.cancelPreview()
	}
	m.renderSeq++
	m.frame++
	m.preview, m.previewRows = "", 0
	cols, rows := m.previewSize()
	if cols == 0 || rows == 0 {
		return nil
	}
	ctx, cancel := m.newOperationContext()
	m.cancelPreview = cancel
	img, seq, protocol := m.session.Image(), m.renderSeq, m.protocol
	return func() tea.Msg {
		if ctx.Err() != nil {
			return imagePreviewMsg{seq: seq}
		}
		// Avoid transmitting a full-resolution PNG for a small terminal preview.
		preview := img
		b := img.Bounds()
		pw, ph := min(cols, imageedit.MaxDimension/8)*8, min(rows, imageedit.MaxDimension/16)*16
		if protocol != termimg.ProtocolNone && (b.Dx() > pw || b.Dy() > ph) {
			var err error
			if int64(b.Dx())*int64(ph) > int64(b.Dy())*int64(pw) {
				preview, err = img.Resize(pw, 0)
			} else {
				preview, err = img.Resize(0, ph)
			}
			if err != nil {
				return imagePreviewMsg{seq: seq}
			}
		}
		output, height := termimg.RenderPixels(preview, cols, rows, protocol)
		if ctx.Err() != nil {
			return imagePreviewMsg{seq: seq}
		}
		graphics := protocol != termimg.ProtocolNone &&
			(strings.Contains(output, "\x1b_G") || strings.Contains(output, "\x1b]1337;") ||
				strings.Contains(output, "\x1bP"))
		if !graphics {
			output = strings.TrimSuffix(strings.TrimPrefix(output, "\x1b[s"), "\x1b[u")
		}
		return imagePreviewMsg{seq: seq, output: output, rows: height, graphics: graphics}
	}
}

func (m *imageModel) View() tea.View {
	return tea.NewView(m.viewContent())
}

func (m *imageModel) viewContent() string {
	clear := ""
	if m.graphicsUsed && m.protocol == termimg.ProtocolKitty {
		clear = termimg.ClearKittyPlacements()
	}
	if m.width < 40 || m.height < 10 {
		message := "Terminal too small (40x10). Resize; q quits."
		if m.prompt == imageConfirmQuit {
			message = "Discard edits? [y/N]"
		}
		lines := []string{message}
		if m.session.Dirty() {
			lines = append(lines, "Unsaved image edits")
		}
		if m.err != "" {
			lines = append(lines, "Error: "+sanitizeTerminalLine(m.err))
		}
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], max(m.width, 0), "")
		}
		view := strings.Join(lines[:min(len(lines), max(m.height, 0))], "\n")
		if m.graphicsUsed {
			view = repaintImageFrame(view, m.frame)
		}
		return clear + view
	}
	img := m.session.SavingImage()
	b := m.session.Image().Bounds()
	dirty := ""
	if m.session.Dirty() {
		dirty = " * unsaved"
	}
	header := m.theme.Chrome.Render(ansi.Truncate("parchment image"+dirty+"  "+sanitizeTerminalLine(img.Path()), m.width, "..."))
	help := "c crop  r resize  u undo  Ctrl+R redo  Ctrl+S save  s save-as  q quit"
	if m.prompt != imageBrowse {
		help = "Tab/Shift+Tab fields  Enter submit (confirm: y/N)  Esc cancel"
	}
	help = m.theme.Muted.Render(ansi.Truncate(help, m.width, "..."))
	metadata := fmt.Sprintf("%dx%d  %s  saved file: %d bytes", b.Dx(), b.Dy(), strings.ToUpper(img.Format()), img.FileSize())
	content := []string{m.theme.Image.Render(ansi.Truncate(metadata, m.width-4, "..."))}
	for _, line := range m.formLines() {
		content = append(content, ansi.Truncate(line, m.width-4, "..."))
	}
	if m.quitting {
		content = nil
	} else if !m.previewGraphics {
		if m.preview != "" {
			content = append(content, m.preview)
		}
	}
	status := m.status
	if status == "" {
		status = "Ready"
	}
	if m.pending {
		status = "Working... Ctrl+C cancels"
	}
	status = sanitizeTerminalLine(status)
	if m.err != "" {
		status = "Error: " + sanitizeTerminalLine(m.err)
	}
	undo, redo := "undo unavailable", "redo unavailable"
	if m.session.CanUndo() {
		undo = "undo available"
	}
	if m.session.CanRedo() {
		redo = "redo available"
	}
	status += "  |  " + undo + "  |  " + redo
	status = ansi.Truncate(status, m.width, "...")
	if m.err != "" {
		status = m.theme.Error.Render(status)
	} else {
		status = m.theme.Muted.Render(status)
	}
	view := chromeLayout(m.width, m.height, header, help, strings.Join(content, "\n"), status, m.theme.BorderColor)
	if m.previewGraphics && m.preview != "" && !m.quitting {
		lines := strings.Split(view, "\n")
		top := 4 + len(m.formLines())
		last := top + m.previewRows - 1
		if last < len(lines)-2 && m.previewRows > 0 {
			payload := strings.TrimSuffix(strings.TrimPrefix(m.preview, "\x1b[s"), "\x1b[u")
			// All covered background rows must precede the graphical paint.
			position := "\x1b[s\r"
			if m.previewRows > 1 {
				position += fmt.Sprintf("\x1b[%dA", m.previewRows-1)
			}
			lines[last] += position + "\x1b[2C" + payload + "\x1b[u"
		}
		view = strings.Join(lines, "\n")
	}
	if m.graphicsUsed {
		view = repaintImageFrame(view, m.frame)
	}
	return clear + view
}
