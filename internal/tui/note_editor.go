package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/tui/editor"
)

// noteEditor holds UI and autosave state for the note editor.
type noteEditor struct {
	snapshot          note.Note
	originalBody      string
	bodyInput         editor.Model
	previewing        bool
	preview           viewport.Model
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
}

func newNoteEditor(undoLimit int) noteEditor {
	body := editor.NewWithUndoLimit(undoLimit)
	body.Placeholder = "Write Markdown…"
	pdfInput := textinput.New()
	pdfInput.CharLimit = 64 << 10
	return noteEditor{
		bodyInput: body,
		preview:   viewport.New(0, 0),
		pdfInput:  pdfInput,
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
	n.snapshot = savedNote
	n.originalBody = savedNote.Body
	n.bodyInput.SetValue(savedNote.Body)
	n.discardWarning = false
}

func (n *noteEditor) resizePreview(width, height int) {
	promptRows := 0
	if n.pdfPrompt != promptNone {
		promptRows = 1
	}
	n.preview.Width, n.preview.Height = max(width-4, 1), max(height-6-promptRows, 1)
	saved := n.snapshot
	saved.Body = n.bodyInput.Value()
	n.preview.SetContent(preview(saved, n.preview.Width))
	n.preview.GotoTop()
}

func (n *noteEditor) resizeEditors(width, height int) {
	inputWidth := max(width-4, 1)
	n.bodyInput.SetWidth(inputWidth)
	n.pdfInput.Width = max(inputWidth-runewidth.StringWidth(n.pdfInput.Prompt), 1)
	promptRows := 0
	if n.pdfPrompt != promptNone {
		promptRows = 1
	}
	n.bodyInput.SetHeight(max(height-6-promptRows, 1))
	if n.previewing {
		n.resizePreview(width, height)
	}
}

func preview(n note.Note, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", sanitizeTerminalLine(n.Title))
	fmt.Fprintf(&b, "\n%s", sanitizeTerminalText(artifactfile.StripPrivateBlocks(n.Body)))
	return renderMarkdown(b.String(), width)
}
