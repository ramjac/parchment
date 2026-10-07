package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/recovery"
)

// toolbarButton is one always-visible toolbar control.
type toolbarButton struct {
	group  string
	label  string
	action string
	hint   string
}

type placedButton struct {
	index, row, x, width int
}

var marginPresets = []struct {
	name string
	mm   int
}{{"Narrow", 13}, {"Normal", 25}, {"Wide", 38}}

var pageSizes = []string{document.PageLetter, document.PageA4, document.PageLegal}
var numberPlacements = []string{document.NumbersNone, document.NumbersLeft, document.NumbersCenter, document.NumbersRight}

func nextOf(values []string, current string) string {
	for i, v := range values {
		if v == current {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

func (s *documentsScreen) marginName() string {
	m := s.layout.Margins
	for _, p := range marginPresets {
		if m.Top == p.mm && m.Right == p.mm && m.Bottom == p.mm && m.Left == p.mm {
			return p.name
		}
	}
	return "Custom"
}

// toolbarButtons lists the controls shown above the editor. Labels for page
// settings show the current value.
func (s *documentsScreen) toolbarButtons() []toolbarButton {
	saveLabel, saveAction := "Save", "save"
	if s.proposing {
		saveLabel, saveAction = "Propose", "propose"
	}
	buttons := []toolbarButton{
		{"File", saveLabel, saveAction, "Ctrl+S"},
		{"File", "Preview", "preview", "F5"},
		{"File", "Close", "close", "Esc"},
		{"Text", "B", "bold", "Alt+B"},
		{"Text", "I", "italic", "Alt+I"},
		{"Text", "S̶", "strike", ""},
		{"Text", "Code", "code", "Alt+C"},
		{"Text", "H1", "h1", "Alt+1"},
		{"Text", "H2", "h2", "Alt+2"},
		{"Text", "H3", "h3", "Alt+3"},
		{"Text", "• List", "bullets", "Alt+L"},
		{"Text", "1. List", "numbers", "Alt+N"},
		{"Text", "Quote", "quote", "Alt+Q"},
		{"Text", "Link", "link", "Alt+K"},
		{"Text", "Rule", "rule", ""},
		{"Text", "Image", "image", "Alt+M"},
		{"Page", "Page break", "pagebreak", "Alt+P"},
		{"Page", "Section", "section", "Alt+S"},
		{"Page", fmt.Sprintf("Cols: %d", s.layout.Columns), "columns", ""},
		{"Page", "Margins: " + s.marginName(), "margins", ""},
		{"Page", "Size: " + s.layout.PageSize, "size", ""},
		{"Page", "Orient: " + s.layout.Orientation, "orientation", ""},
		{"Page", "Header", "header", ""},
		{"Page", "Footer", "footer", ""},
		{"Page", "Page #: " + s.layout.PageNumbers, "numbers-pos", ""},
	}
	if s.proposing {
		kept := buttons[:0]
		for _, button := range buttons {
			if button.action != "image" {
				kept = append(kept, button)
			}
		}
		buttons = kept
	}
	return buttons
}

// buttonLayout wraps the toolbar to the available width. It is used for both
// rendering and mouse hit-testing so they cannot disagree.
func (s *documentsScreen) buttonLayout(width int) ([]placedButton, int) {
	buttons := s.toolbarButtons()
	var placed []placedButton
	row, x, group := 0, 0, ""
	for i, b := range buttons {
		w := runewidth.StringWidth(b.label) + 2
		prefix := 0
		if b.group != group {
			if group != "" {
				row++
			}
			group, x, prefix = b.group, 0, runewidth.StringWidth(b.group)+1
			x = prefix
		}
		if x+w > width && x > prefix {
			row++
			x = 0
		}
		placed = append(placed, placedButton{index: i, row: row, x: x, width: w})
		x += w + 1
	}
	return placed, row + 1
}

func (s *documentsScreen) toolbarRows() int {
	_, rows := s.buttonLayout(max(s.width, 20))
	return rows
}

// toolbarTop is the screen row of the toolbar's first line: header, state.
const toolbarTop = 2

func (s *documentsScreen) toolbarView() string {
	placed, rows := s.buttonLayout(max(s.width, 20))
	buttons := s.toolbarButtons()
	normal := lipgloss.NewStyle().Foreground(s.theme.primary).Background(lipgloss.AdaptiveColor{Light: "#e7e3f5", Dark: "#2f2b45"})
	active := normal.Reverse(true).Bold(true)
	lines := make([]string, rows)
	groupSeen := map[int]string{}
	for _, p := range placed {
		b := buttons[p.index]
		style := normal
		if s.focus == focusToolbar && p.index == s.toolbarIndex {
			style = active
		}
		if p.x > 0 && p.x == runewidth.StringWidth(b.group)+1 && groupSeen[p.row] == "" {
			groupSeen[p.row] = b.group
			lines[p.row] += b.group + " "
		} else if p.x > 0 && lines[p.row] != "" {
			lines[p.row] += " "
		}
		lines[p.row] += style.Render(" " + b.label + " ")
	}
	return strings.Join(lines, "\n")
}

func (s *documentsScreen) startCreate() tea.Cmd {
	id, err := newRecoveryID()
	if err != nil {
		s.errMessage = "Start autosave: " + err.Error()
		return nil
	}
	s.creating, s.proposing = true, false
	s.autosaveID = id
	s.snapshot = document.Document{}
	s.layout = document.DefaultLayout()
	s.images = nil
	return s.beginEditor("", "")
}

func (s *documentsScreen) startEdit(d document.Document) tea.Cmd {
	// Refuse documents the editor widgets would truncate or alter; saving
	// after any edit would otherwise overwrite the full stored text.
	s.titleInput.SetValue(d.Title)
	s.body.SetValue(d.Body)
	if s.titleInput.Value() != d.Title || s.body.Value() != d.Body {
		s.titleInput.SetValue("")
		s.body.SetValue("")
		s.errMessage = "This document exceeds editor limits or contains text the editor cannot preserve; edit it with `parchment document edit`"
		s.status = ""
		return nil
	}
	id, err := newRecoveryID()
	if err != nil {
		s.errMessage = "Start autosave: " + err.Error()
		return nil
	}
	s.creating, s.proposing = false, false
	s.autosaveID = id
	s.snapshot = d
	s.layout = d.Layout
	if s.layout == (document.Layout{}) {
		s.layout = document.DefaultLayout()
	}
	s.images = d.Images
	return s.beginEditor(d.Title, d.Body)
}

func (s *documentsScreen) beginEditor(title, body string) tea.Cmd {
	s.startDocumentAutosaveSession()
	s.mode = documentEditing
	s.titleInput.SetValue(title)
	s.body.SetValue(body)
	s.body.CursorStart()
	s.original = s.currentDraft()
	s.prompt, s.previewing, s.discardWarning = promptNone, false, false
	s.toolbarIndex = 0
	s.errMessage, s.status = "", ""
	s.layoutEditor()
	return tea.Batch(s.focusTitle(), tea.EnableMouseCellMotion, s.scheduleDocumentAutosave(s.autosaveSession))
}

func (s *documentsScreen) stopEditing(status string) tea.Cmd {
	s.mode, s.creating, s.proposing, s.prompt, s.previewing, s.discardWarning = documentBrowsing, false, false, promptNone, false, false
	s.status, s.errMessage = status, ""
	s.stopDocumentAutosave()
	cmds := []tea.Cmd{tea.DisableMouse}
	if s.recoveryStore != nil && s.autosaveID != "" {
		store, id := s.recoveryStore, s.autosaveID
		cmds = append(cmds, func() tea.Msg {
			return documentRecoveryDeletedMsg{err: store.DeleteRecovery(context.Background(), id)}
		})
	}
	return tea.Batch(cmds...)
}

func (s *documentsScreen) restoreRecovery(draft recovery.Draft, data documentRecoveryData) tea.Cmd {
	s.autosaveID = draft.ID
	s.creating, s.proposing, s.snapshot = draft.Created, draft.Proposal, data.Snapshot
	s.snapshot.Blocks = cloneRawMessages(data.SnapshotBlocks)
	s.layout, s.images = data.Draft.Layout, data.Draft.Images
	if s.creating {
		s.snapshot = document.Document{}
	}
	cmd := s.beginEditor(data.Draft.Title, data.Draft.Body)
	if s.creating {
		s.original = editorDraft{}
	} else {
		s.original = editorDraft{
			title: s.snapshot.Title, body: s.snapshot.Body,
			layout: s.snapshot.Layout, images: imageKey(s.snapshot.Images),
		}
	}
	s.status = "Recovered unsaved document draft"
	return cmd
}

func (s *documentsScreen) startDocumentAutosaveSession() {
	s.autosaveSession++
	if s.autosaveCancel != nil {
		s.autosaveCancel()
		s.autosaveCancel = nil
	}
}

func (s *documentsScreen) stopDocumentAutosave() {
	s.autosaveSession++
	if s.autosaveCancel != nil {
		s.autosaveCancel()
		s.autosaveCancel = nil
	}
}

func (s *documentsScreen) scheduleDocumentAutosave(session uint64) tea.Cmd {
	if s.recoveryStore == nil || s.autosaveScheduler == nil {
		return nil
	}
	return s.autosaveScheduler(session)
}

func (s *documentsScreen) saveDocumentRecovery(session uint64) tea.Cmd {
	if s.recoveryStore == nil {
		return nil
	}
	if !s.dirty() {
		return s.scheduleDocumentAutosave(session)
	}
	data := documentRecoveryData{
		Snapshot: s.snapshot, SnapshotBlocks: cloneRawMessages(s.snapshot.Blocks),
		Draft: document.Draft{
			Title: s.titleInput.Value(), Body: s.body.Value(),
			Layout: s.layout, Images: cloneDocumentImages(s.images),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.autosaveCancel = cancel
	store := s.recoveryStore
	draft := recovery.Draft{
		ID: s.autosaveID, Kind: "document", Artifact: s.snapshot.ID,
		Created: s.creating, Proposal: s.proposing, Title: s.titleInput.Value(),
		UpdatedAt: time.Now().UTC(),
	}
	return func() tea.Msg {
		encoded, err := json.Marshal(data)
		if err == nil {
			draft.Data = encoded
			err = store.SaveRecovery(ctx, draft)
		}
		return documentAutosaveFinishedMsg{session: session, err: err}
	}
}

func cloneDocumentImages(source []document.Image) []document.Image {
	images := make([]document.Image, len(source))
	for i, image := range source {
		images[i] = image
		images[i].Data = append([]byte(nil), image.Data...)
	}
	return images
}

func (s *documentsScreen) focusTitle() tea.Cmd {
	s.focus = focusTitle
	s.body.Blur()
	return s.titleInput.Focus()
}

func (s *documentsScreen) focusBody() tea.Cmd {
	s.focus = focusBody
	s.titleInput.Blur()
	return s.body.Focus()
}

func (s *documentsScreen) focusToolbar() {
	s.focus = focusToolbar
	s.titleInput.Blur()
	s.body.Blur()
}

func (s *documentsScreen) layoutEditor() {
	if s.width == 0 {
		return
	}
	s.titleInput.Width = max(s.width-10, 10)
	s.body.SetWidth(max(s.width, 10))
	// header, state, toolbar, blank, title, blank, status line, and a spare row.
	s.body.SetHeight(max(s.height-s.toolbarRows()-7, 3))
}

func (s *documentsScreen) editorDocument() document.Document {
	d := document.Document{Body: s.body.Value(), Layout: s.layout, Images: s.images}
	d.Title = s.titleInput.Value()
	return d
}

func (s *documentsScreen) updateEditorKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if s.prompt != promptNone {
		return s.updatePrompt(msg)
	}
	if s.previewing {
		switch key {
		case "esc", "f5", "q":
			s.previewing = false
		case "right", "pgdown", "n", "]":
			s.editPage = min(s.editPage+1, len(s.editPages)-1)
		case "left", "pgup", "p", "[":
			s.editPage = max(s.editPage-1, 0)
		}
		return nil
	}
	if key != "esc" {
		s.discardWarning = false
	}
	switch key {
	case "ctrl+s":
		return s.save()
	case "f5":
		s.openPreview()
		return nil
	case "esc":
		if s.focus == focusToolbar {
			return s.focusBody()
		}
		if s.dirty() && !s.discardWarning {
			s.discardWarning = true
			s.status = "Unsaved changes: Ctrl+S saves, Esc again discards"
			return nil
		}
		return s.stopEditing("Edit closed")
	case "tab":
		switch s.focus {
		case focusTitle:
			return s.focusBody()
		case focusBody:
			s.focusToolbar()
			return nil
		}
		return s.focusTitle()
	case "f2":
		s.focusToolbar()
		return nil
	}
	if action, ok := altActions[key]; ok {
		return s.act(action)
	}
	if s.focus == focusToolbar {
		return s.updateToolbarKey(key)
	}
	var cmd tea.Cmd
	if s.focus == focusTitle {
		if key == "enter" {
			return s.focusBody()
		}
		s.titleInput, cmd = s.titleInput.Update(msg)
	} else {
		s.body, cmd = s.body.Update(msg)
	}
	return cmd
}

var altActions = map[string]string{
	"alt+b": "bold", "alt+i": "italic", "alt+c": "code", "alt+1": "h1", "alt+2": "h2", "alt+3": "h3",
	"alt+l": "bullets", "alt+n": "numbers", "alt+q": "quote", "alt+k": "link", "alt+m": "image",
	"alt+p": "pagebreak", "alt+s": "section",
}

func (s *documentsScreen) updateToolbarKey(key string) tea.Cmd {
	count := len(s.toolbarButtons())
	switch key {
	case "left", "shift+tab", "up":
		s.toolbarIndex = (s.toolbarIndex + count - 1) % count
	case "right", "down":
		s.toolbarIndex = (s.toolbarIndex + 1) % count
	case "home":
		s.toolbarIndex = 0
	case "end":
		s.toolbarIndex = count - 1
	case "enter", " ":
		return s.act(s.toolbarButtons()[s.toolbarIndex].action)
	}
	return nil
}

func (s *documentsScreen) updateMouse(msg tea.MouseMsg) tea.Cmd {
	if s.mode != documentEditing || s.pending || s.prompt != promptNone || s.previewing {
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	placed, _ := s.buttonLayout(max(s.width, 20))
	buttons := s.toolbarButtons()
	for _, p := range placed {
		if msg.Y == toolbarTop+p.row && msg.X >= p.x && msg.X < p.x+p.width {
			s.toolbarIndex = p.index
			return s.act(buttons[p.index].action)
		}
	}
	return nil
}

func (s *documentsScreen) save() tea.Cmd {
	if s.pending {
		return nil
	}
	s.stopDocumentAutosave()
	draft := document.Draft{Title: s.titleInput.Value(), Body: s.body.Value(), Layout: s.layout, Images: s.images}
	s.pending = true
	s.errMessage = ""
	ctx := s.startOperation()
	service, creating, proposing, snapshot := s.service, s.creating, s.proposing, s.snapshot
	store, recoveryID := s.recoveryStore, s.autosaveID
	return func() tea.Msg {
		if proposing {
			change, err := service.Propose(ctx, snapshot, "TUI edit", draft)
			var cleanupErr error
			if err == nil && store != nil {
				cleanupErr = store.DeleteRecovery(context.Background(), recoveryID)
			}
			return documentProposedMsg{change: change, err: err, cleanupErr: cleanupErr}
		}
		var d document.Document
		var err error
		if creating {
			d, err = service.Create(ctx, draft)
		} else {
			d, err = service.Save(ctx, snapshot, draft)
		}
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), recoveryID)
		}
		return documentSavedMsg{document: d, err: err, cleanupErr: cleanupErr}
	}
}

func (s *documentsScreen) openPreview() {
	pages, err := document.Paginate(s.editorDocument())
	if err != nil {
		s.errMessage = err.Error()
		return
	}
	s.errMessage = ""
	s.editPages, s.editPage, s.previewing = pages, 0, true
}

// act runs one toolbar action. Layout changes modify editor state only; they
// are persisted with the document when it is saved.
func (s *documentsScreen) act(action string) tea.Cmd {
	s.errMessage, s.status = "", ""
	switch action {
	case "save":
		return s.save()
	case "propose":
		return s.save()
	case "image":
		if s.proposing {
			s.errMessage = "Embedded images cannot be added to a proposal"
			return nil
		}
		return s.openPrompt(promptImage, "Image file path (PNG, JPEG, GIF): ", "")
	case "preview":
		s.openPreview()
		return nil
	case "close":
		if s.dirty() && !s.discardWarning {
			s.discardWarning = true
			s.status = "Unsaved changes: Ctrl+S saves, Close again discards"
			return nil
		}
		return s.stopEditing("Edit closed")
	case "columns":
		s.layout.Columns = s.layout.Columns%document.MaxColumns + 1
	case "margins":
		next := marginPresets[0].mm
		for i, p := range marginPresets {
			if s.marginName() == p.name {
				next = marginPresets[(i+1)%len(marginPresets)].mm
			}
		}
		s.layout.Margins = document.Margins{Top: next, Right: next, Bottom: next, Left: next}
	case "size":
		s.layout.PageSize = nextOf(pageSizes, s.layout.PageSize)
	case "orientation":
		if s.layout.Orientation == document.Portrait {
			s.layout.Orientation = document.Landscape
		} else {
			s.layout.Orientation = document.Portrait
		}
	case "numbers-pos":
		s.layout.PageNumbers = nextOf(numberPlacements, s.layout.PageNumbers)
	case "header":
		return s.openPrompt(promptHeader, "Header (left|center|right; {title} {page} {pages}): ", s.layout.Header)
	case "footer":
		return s.openPrompt(promptFooter, "Footer (left|center|right; {title} {page} {pages}): ", s.layout.Footer)
	case "link":
		return s.openPrompt(promptLink, "Link URL: ", "https://")
	case "section":
		return s.openPrompt(promptSection, "Section columns 1-4 (add c for continuous, e.g. 2c): ", fmt.Sprint(s.layout.Columns))
	case "pagebreak":
		s.insertBlock(document.PageBreakMarkup)
	case "rule":
		s.insertBlock("---")
	case "bold":
		s.wrap("**", "**")
	case "italic":
		s.wrap("*", "*")
	case "strike":
		s.wrap("~~", "~~")
	case "code":
		s.wrap("`", "`")
	case "h1":
		s.linePrefix("# ")
	case "h2":
		s.linePrefix("## ")
	case "h3":
		s.linePrefix("### ")
	case "bullets":
		s.linePrefix("- ")
	case "numbers":
		s.linePrefix("1. ")
	case "quote":
		s.linePrefix("> ")
	}
	if textAction(action) {
		return s.focusBody()
	}
	return nil
}

func textAction(action string) bool {
	switch action {
	case "bold", "italic", "strike", "code", "h1", "h2", "h3", "bullets", "numbers", "quote", "rule", "pagebreak":
		return true
	}
	return false
}

func (s *documentsScreen) openPrompt(kind promptKind, label, value string) tea.Cmd {
	s.prompt = kind
	s.promptInput.Prompt = label
	s.promptInput.SetValue(value)
	s.promptInput.CursorEnd()
	return s.promptInput.Focus()
}

func (s *documentsScreen) updatePrompt(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.prompt = promptNone
		s.promptInput.Blur()
		return nil
	case "enter":
		kind, value := s.prompt, strings.TrimSpace(s.promptInput.Value())
		s.prompt = promptNone
		s.promptInput.Blur()
		return s.finishPrompt(kind, value)
	}
	var cmd tea.Cmd
	s.promptInput, cmd = s.promptInput.Update(msg)
	return cmd
}

func (s *documentsScreen) finishPrompt(kind promptKind, value string) tea.Cmd {
	switch kind {
	case promptHeader, promptFooter:
		if hasControl(value) {
			s.errMessage = "Text contains control characters"
			return nil
		}
		old := s.layout
		if kind == promptHeader {
			s.layout.Header = value
		} else {
			s.layout.Footer = value
		}
		if err := s.layout.Validate(); err != nil {
			s.layout, s.errMessage = old, err.Error()
		}
	case promptLink:
		if value == "" {
			return nil
		}
		s.insertText("[](" + value + ")")
		s.moveLeft(len([]rune(value)) + 3)
		return s.focusBody()
	case promptImage:
		if value == "" {
			return nil
		}
		s.pending = true
		return readImageCommand(s.startOperation(), value)
	case promptSection:
		trimmed := strings.TrimSuffix(value, "c")
		continuous := trimmed != value
		columns, err := strconv.Atoi(trimmed)
		if err != nil || columns < 1 || columns > document.MaxColumns {
			s.errMessage = "Enter a column count from 1 to 4, optionally followed by c"
			return nil
		}
		s.insertBlock(document.SectionBreakMarkup(columns, continuous))
		return s.focusBody()
	}
	return nil
}

func (s *documentsScreen) moveLeft(n int) {
	for range n {
		s.body, _ = s.body.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
}

// prepareBody focuses the body widget itself, not just the focus marker, so
// cursor movement and later typing reach the textarea.
func (s *documentsScreen) prepareBody() {
	if s.focus != focusBody || !s.body.Focused() {
		s.focusBody()
	}
}

func (s *documentsScreen) insertText(text string) {
	s.prepareBody()
	s.body.InsertString(text)
}

// wrap inserts paired markers with the cursor between them.
func (s *documentsScreen) wrap(open, close string) {
	s.insertText(open + close)
	s.moveLeft(len([]rune(close)))
}

// linePrefix starts the current line with a Markdown marker.
func (s *documentsScreen) linePrefix(prefix string) {
	s.prepareBody()
	s.body.CursorStart()
	s.body.InsertString(prefix)
}

// insertBlock places a block on its own paragraph.
func (s *documentsScreen) insertBlock(text string) {
	s.prepareBody()
	prefix := "\n\n"
	if s.body.Value() == "" || s.body.LineInfo().ColumnOffset == 0 && s.body.LineInfo().StartColumn == 0 && s.currentLineEmpty() {
		prefix = ""
	}
	s.body.InsertString(prefix + text + "\n\n")
}

func (s *documentsScreen) currentLineEmpty() bool {
	lines := strings.Split(s.body.Value(), "\n")
	row := s.body.Line()
	return row < len(lines) && strings.TrimSpace(lines[row]) == ""
}

func (s *documentsScreen) editorView(header string) string {
	state := "Editing"
	if s.proposing {
		state = "Proposing a change"
	}
	if s.dirty() {
		state += " • unsaved"
	}
	saveHint := "Ctrl+S saves"
	if s.proposing {
		saveHint = "Ctrl+S records proposal"
	}
	state += "  ·  Tab switches Title/Body/Toolbar  ·  F2 toolbar  ·  " + saveHint
	if s.previewing {
		page := ""
		if len(s.editPages) > 0 {
			page = pageText(s.editPages[s.editPage], max(s.width, 1))
		}
		return header + "\nPrint preview  ·  page " + fmt.Sprintf("%d/%d", s.editPage+1, len(s.editPages)) +
			"  ·  ←/→ pages  ·  Esc returns\n" + page
	}
	title := s.titleInput
	title.SetValue(sanitizeTerminalLine(title.Value()))
	body := s.body
	body.SetValue(sanitizeTerminalText(body.Value()))
	var b strings.Builder
	b.WriteString(header + "\n" + state + "\n" + s.toolbarView() + "\n\n" + title.View() + "\n")
	if s.prompt != promptNone {
		b.WriteString(s.promptInput.View())
	}
	b.WriteString("\n" + body.View())
	return b.String() + s.statusLine()
}
