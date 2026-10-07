package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/recovery"
)

// noteRecoveryData is the autosaved note editor state: the saved note the
// edits started from and the unsaved body.
type noteRecoveryData struct {
	Snapshot       note.Note                  `json:"snapshot"`
	SnapshotBody   string                     `json:"snapshot_body"`
	SnapshotBlocks map[string]json.RawMessage `json:"snapshot_blocks,omitempty"`
	Body           string                     `json:"body"`
}

func (m *Model) startAutosaveSession() {
	m.autosaveSession++
	if m.autosaveCancel != nil {
		m.autosaveCancel()
		m.autosaveCancel = nil
	}
}

func (m *Model) scheduleAutosave(session uint64) tea.Cmd {
	if m.recoveryStore == nil || m.autosaveScheduler == nil {
		return nil
	}
	return m.autosaveScheduler(session)
}

func (m *Model) stopAutosave() { m.startAutosaveSession() }

func (m *Model) saveNoteRecovery(session uint64) tea.Cmd {
	if m.recoveryStore == nil {
		return nil
	}
	if !m.dirty() {
		if !m.draftStored {
			return m.scheduleAutosave(session)
		}
		// The edits were reverted after a draft was autosaved; remove it so a
		// crash does not offer changes the user already undid.
		store, path := m.recoveryStore, m.path
		return func() tea.Msg {
			return autosaveFinishedMsg{session: session, err: store.DeleteRecovery(context.Background(), path), cleared: true}
		}
	}
	m.draftStored = true
	data := noteRecoveryData{
		Snapshot: m.snapshot, SnapshotBody: m.snapshot.Body,
		SnapshotBlocks: cloneRawMessages(m.snapshot.Blocks),
		Body:           m.bodyInput.Value(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.autosaveCancel = cancel
	store := m.recoveryStore
	draft := recovery.Draft{
		Path: m.path, Kind: string(artifact.NoteKind), UpdatedAt: time.Now().UTC(),
	}
	return func() tea.Msg {
		encoded, err := json.Marshal(data)
		if err == nil {
			draft.Data = encoded
			err = store.SaveRecovery(ctx, draft)
		}
		return autosaveFinishedMsg{session: session, err: err}
	}
}

func cloneRawMessages(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	return clone
}

// recoverDraft opens the editor with the autosaved draft for this file. The
// draft keeps the saved state it started from, so saving still detects edits
// made to the file after the draft was written.
func (m *Model) recoverDraft() tea.Cmd {
	draft, opened := m.opened.draft, m.opened
	if artifact.Kind(draft.Kind) != opened.kind {
		m.errMessage = fmt.Sprintf("The autosaved draft is for a %s, but this file is a %s; press d to discard it", draft.Kind, opened.kind)
		return nil
	}
	switch opened.kind {
	case artifact.NoteKind:
		var data noteRecoveryData
		if err := json.Unmarshal(draft.Data, &data); err != nil {
			m.errMessage = "Decode autosaved note: " + err.Error() + "; press d to discard it"
			return nil
		}
		snapshot := data.Snapshot
		snapshot.Body, snapshot.Blocks, snapshot.Path = data.SnapshotBody, cloneRawMessages(data.SnapshotBlocks), m.path
		if !m.startEdit(snapshot) {
			m.stage = stageFailed
			return nil
		}
		m.bodyInput.SetValue(data.Body)
		m.draftStored = true
		m.stage = stageNote
		m.status = "Recovered unsaved note draft"
		if !note.Equal(snapshot, opened.note) {
			m.status += "; the file changed after the draft was saved, so saving will report a conflict"
		}
		return tea.Batch(m.bodyInput.Focus(), m.scheduleAutosave(m.autosaveSession))
	case artifact.DocumentKind:
		var data documentRecoveryData
		if err := json.Unmarshal(draft.Data, &data); err != nil {
			m.errMessage = "Decode autosaved document: " + err.Error() + "; press d to discard it"
			return nil
		}
		cmd, ok := m.documents.restoreRecovery(data)
		if !ok {
			m.stage, m.errMessage = stageFailed, m.documents.errMessage
			return nil
		}
		if !document.Equal(m.documents.snapshot, opened.document) {
			m.documents.status += "; the file changed after the draft was saved, so saving will report a conflict"
		}
		m.stage = stageDocument
		return cmd
	}
	return nil
}
