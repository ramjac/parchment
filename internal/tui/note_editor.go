package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/tui/editor"
	tuitheme "example.com/parchment/internal/tui/theme"
)

// noteEditor holds UI and autosave state for the note editor.
type noteEditor struct {
	snapshot          note.Note
	textSnapshot      *note.TextSnapshot
	originalBody      string
	bodyInput         editor.Model
	previewing        bool
	preview           viewport.Model
	previewContent    string
	previewMatches    []renderedMatch
	previewMatch      int
	discardWarning    bool
	canUndo           bool
	canRedo           bool
	autosaveSession   uint64
	autosaveCancel    context.CancelFunc
	draftStored       bool
	autosaveScheduler func(uint64) tea.Cmd
	pdfStore          PDFExportStore
	lastPDFPath       string
	pdfPrompt         promptKind
	pdfInput          textinput.Model
	pdfOverwritePath  string
	theme             tuitheme.Theme
	find              findBar
}

func newNoteEditor(undoLimit int, theme tuitheme.Theme) noteEditor {
	body := editor.NewWithUndoLimit(undoLimit)
	body.SetTheme(theme)
	body.Placeholder = "Write Markdown…"
	pdfInput := textinput.New()
	pdfInput.CharLimit = 64 << 10
	return noteEditor{
		bodyInput:    body,
		preview:      viewport.New(),
		previewMatch: -1,
		pdfInput:     pdfInput,
		theme:        theme,
		find:         newFindBar(theme),
		autosaveScheduler: func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return autosaveTickMsg{session: session}
			})
		},
	}
}

func (n *noteEditor) startAutosaveSession() {
	n.autosaveSession++
	if n.autosaveCancel != nil {
		n.autosaveCancel()
		n.autosaveCancel = nil
	}
}

func (n *noteEditor) stopAutosave() { n.startAutosaveSession() }

// loadNote makes note saved state of the editor.
func (n *noteEditor) loadNote(savedNote note.Note) {
	n.markSaved(savedNote)
	n.bodyInput.SetValue(savedNote.Body)
}

func (n *noteEditor) markSaved(savedNote note.Note) {
	n.textSnapshot = nil
	n.snapshot = savedNote
	n.originalBody = savedNote.Body
	n.discardWarning = false
}

func (n *noteEditor) markTextSaved(snapshot note.TextSnapshot) {
	n.markSaved(snapshot.Note)
	copy := snapshot.Clone()
	n.textSnapshot = &copy
	n.originalBody = snapshot.Document.Text
}

func (n *noteEditor) resizePreview(width, height int) {
	offset, current := n.preview.YOffset(), n.previewMatch
	promptRows := 0
	if n.pdfPrompt != promptNone {
		promptRows = 1
	}
	n.preview.SetWidth(max(width-4, 1))
	n.preview.SetHeight(max(height-6-promptRows, 1))
	saved := n.snapshot
	saved.Body = n.bodyInput.Value()
	n.previewContent = preview(saved, n.preview.Width(), n.theme)
	n.previewMatches = renderedMatches(strings.Split(n.previewContent, "\n"), n.find.query(), 0)
	n.previewMatch = min(max(current, 0), len(n.previewMatches)-1)
	n.renderPreviewContent(offset)
}

func (n *noteEditor) resizeEditors(width, height int) {
	inputWidth := max(width-4, 1)
	n.bodyInput.SetWidth(inputWidth)
	n.pdfInput.SetWidth(max(inputWidth-runewidth.StringWidth(n.pdfInput.Prompt), 1))
	promptRows := 0
	if n.pdfPrompt != promptNone {
		promptRows = 1
	}
	n.bodyInput.SetHeight(max(height-6-promptRows, 1))
	if n.previewing {
		n.resizePreview(width, height)
	}
}

func (n *noteEditor) updateFindKey(msg tea.Msg) tea.Cmd {
	action, cmd := n.find.update(msg)
	if n.previewing {
		switch action {
		case findChanged:
			n.searchPreview(n.find.query())
		case findNext:
			n.movePreviewMatch(1)
			n.find.blur()
		case findPrevious:
			n.movePreviewMatch(-1)
			n.find.blur()
		case findClosed:
			n.searchPreview("")
		}
		return cmd
	}
	switch action {
	case findChanged:
		n.bodyInput.SetSearch(n.find.query())
	case findNext:
		n.bodyInput.MoveToMatch(1)
	case findPrevious:
		n.bodyInput.MoveToMatch(-1)
	case findClosed:
		n.bodyInput.ClearSearch()
		return tea.Batch(cmd, n.bodyInput.Focus())
	}
	return cmd
}

func (n *noteEditor) openFind(width int) tea.Cmd {
	n.bodyInput.Blur()
	if n.previewing {
		n.searchPreview(n.find.query())
	} else {
		n.bodyInput.SetSearch(n.find.query())
	}
	return n.find.open(width)
}

func (n *noteEditor) findStatus(width int) string {
	return n.find.view(n.bodyInput.MatchCount(), n.bodyInput.ActiveMatch(), width)
}

func (n *noteEditor) searchPreview(query string) {
	n.previewMatches = renderedMatches(strings.Split(n.previewContent, "\n"), query, 0)
	n.previewMatch = -1
	if len(n.previewMatches) > 0 {
		n.previewMatch = 0
	}
	n.renderPreviewContent(n.preview.YOffset())
	if n.previewMatch >= 0 {
		n.preview.SetYOffset(n.previewMatches[n.previewMatch].row)
	}
}

func (n *noteEditor) movePreviewMatch(step int) {
	if len(n.previewMatches) == 0 {
		return
	}
	n.previewMatch = (n.previewMatch + step) % len(n.previewMatches)
	if n.previewMatch < 0 {
		n.previewMatch += len(n.previewMatches)
	}
	n.preview.SetYOffset(n.previewMatches[n.previewMatch].row)
}

func (n *noteEditor) renderPreviewContent(offset int) {
	content := n.previewContent
	if query := n.find.query(); query != "" {
		content = highlightRenderedText(content, query, n.theme)
	}
	n.preview.SetContent(content)
	n.preview.SetYOffset(offset)
}

func preview(n note.Note, width int, theme tuitheme.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", sanitizeTerminalLine(n.Title))
	fmt.Fprintf(&b, "\n%s", sanitizeTerminalText(artifactfile.StripPrivateBlocks(n.Body)))
	return renderMarkdown(b.String(), width, theme)
}
