package tui

import (
	"bytes"
	"context"
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/note"
	"example.com/parchment/internal/notetext"
	"example.com/parchment/internal/recovery"
)

// A draft can supply an encoding only for the exact saved bytes it describes.
func openNoteText(ctx context.Context, notes *note.Service, path, encoding string, draft recovery.Draft, hasDraft bool) (note.TextSnapshot, error, error) {
	snapshot, err := notes.OpenText(ctx, path, encoding)
	if err == nil || !hasDraft {
		return snapshot, nil, err
	}
	var data noteRecoveryData
	if json.Unmarshal(draft.Data, &data) != nil ||
		(data.Version != 0 && (!data.Plain || data.Version != 2)) {
		return snapshot, nil, err
	}
	current, readErr := notes.Get(ctx, path)
	if readErr != nil {
		return snapshot, nil, err
	}
	if data.Version == 2 && (encoding == "" || encoding == notetext.Auto) && bytes.Equal([]byte(current.Body), data.Raw) {
		if decoded, decodeErr := notes.OpenText(ctx, path, data.Format.Encoding); decodeErr == nil {
			return decoded, nil, nil
		}
	}
	return note.TextSnapshot{Note: current}, err, nil
}

func (m *Model) recoverPlainNote(data noteRecoveryData) tea.Cmd {
	if m.noteEncoding != "" && m.noteEncoding != notetext.Auto && m.noteEncoding != data.Format.Encoding {
		m.errMessage = "The encoding override conflicts with the autosaved note; reopen without --encoding or discard the draft"
		return nil
	}
	n := data.Snapshot
	n.Path, n.Blocks = m.path, cloneRawMessages(data.SnapshotBlocks)
	snapshot, err := note.RestoreTextSnapshot(n, data.Raw, data.Format)
	if err != nil {
		m.errMessage = "Decode autosaved note baseline: " + err.Error() + "; press d to discard it"
		return nil
	}
	doc := notetext.Document{Text: data.Body, Endings: data.Endings}
	if _, err := doc.Source(); err != nil {
		m.errMessage = "Decode autosaved note text: " + err.Error() + "; press d to discard it"
		return nil
	}
	if !m.startTextEdit(snapshot) {
		m.stage = stageFailed
		return nil
	}
	if err := m.bodyInput.SetDocument(doc); err != nil {
		m.stage, m.errMessage = stageFailed, err.Error()
		return nil
	}
	m.draftStored = true
	m.stage, m.status = stageNote, "Recovered unsaved note draft"
	if m.opened.text == nil || !bytes.Equal(snapshot.Raw, m.opened.text.Raw) {
		m.status += "; the file changed after the draft was saved, so saving will report a conflict"
	}
	return tea.Batch(m.bodyInput.Focus(), m.scheduleAutosave(m.autosaveSession))
}
