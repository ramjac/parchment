package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

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
}

func newNoteEditor() noteEditor {
	body := editor.New()
	body.Placeholder = "Write Markdown…"
	return noteEditor{
		bodyInput: body,
		preview:   viewport.New(0, 0),
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
	n.preview.Width, n.preview.Height = max(width-2, 1), max(height-4, 1)
	saved := n.snapshot
	saved.Body = n.bodyInput.Value()
	n.preview.SetContent(preview(saved))
	n.preview.GotoTop()
}

func (n *noteEditor) resizeEditors(width, height int) {
	inputWidth := max(width, 10)
	n.bodyInput.SetWidth(inputWidth)
	n.bodyInput.SetHeight(max(height-4, 3))
	if n.previewing {
		n.resizePreview(width, height)
	}
}

func preview(n note.Note) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", sanitizeTerminalLine(n.Title))
	fmt.Fprintf(&b, "\n%s", sanitizeTerminalText(artifactfile.StripPrivateBlocks(n.Body)))
	return b.String()
}
