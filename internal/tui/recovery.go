package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/notetext"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
)

// spreadsheetRecoveryData is the autosaved spreadsheet editor state: the saved
// workbook the edits started from, dirty sheet state, and cursor/cell input positions.
type spreadsheetRecoveryData struct {
	Baseline         spreadsheet.Spreadsheet    `json:"baseline"`
	BaselineBody     string                     `json:"baseline_body,omitempty"`
	BaselineBlocks   map[string]json.RawMessage `json:"baseline_blocks,omitempty"`
	Dirty            spreadsheet.Spreadsheet    `json:"dirty"`
	DirtyBody        string                     `json:"dirty_body,omitempty"`
	DirtyBlocks      map[string]json.RawMessage `json:"dirty_blocks,omitempty"`
	ActiveSheet      int                        `json:"active_sheet"`
	CursorRow        int                        `json:"cursor_row"`
	CursorCol        int                        `json:"cursor_col"`
	Editing          bool                       `json:"editing,omitempty"`
	PendingCellInput string                     `json:"pending_cell_input,omitempty"`
}

// presentationRecoveryData is the autosaved presentation editor state: the saved
// presentation the edits started from and the unsaved Markdown source.
type presentationRecoveryData struct {
	Baseline presentation.Presentation `json:"baseline"`
	Source   string                    `json:"source"`
	Workbook spreadsheet.Workbook      `json:"workbook,omitempty"`
}

// noteRecoveryData is the autosaved note editor state: the saved note the
// edits started from and the unsaved body.
type noteRecoveryData struct {
	Version        int                        `json:"version,omitempty"`
	Plain          bool                       `json:"plain,omitempty"`
	Raw            []byte                     `json:"raw,omitempty"`
	Format         notetext.Format            `json:"format,omitempty"`
	Endings        []notetext.Ending          `json:"endings,omitempty"`
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
	if m.textSnapshot != nil {
		data.Version, data.Plain = 2, true
		data.Raw = append([]byte(nil), m.textSnapshot.Raw...)
		data.Format = m.textSnapshot.Format
		data.SnapshotBody = ""
		data.Endings = m.bodyInput.Document().Endings
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
		if data.Version != 0 && data.Version != 2 {
			m.errMessage = "Unsupported autosaved note version; press d to discard it"
			return nil
		}
		if data.Plain {
			return m.recoverPlainNote(data)
		}
		if data.Version == 0 && ((opened.text != nil && opened.text.Plain) || opened.noteOpenErr != nil) {
			raw := []byte(data.SnapshotBody)
			_, format, err := notetext.Decode(raw, notetext.Auto)
			if err != nil {
				m.errMessage = "Decode legacy note recovery baseline: " + err.Error() + "; press d to discard it"
				return nil
			}
			data.Plain = true
			data.Raw, data.Format = raw, format
			if format.BOM {
				data.Body = strings.TrimPrefix(data.Body, "\ufeff")
			}
			draft := notetext.Split(data.Body)
			data.Endings, data.Body = draft.Endings, draft.Text
			return m.recoverPlainNote(data)
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
	case artifact.PresentationKind:
		var data presentationRecoveryData
		if err := json.Unmarshal(draft.Data, &data); err != nil {
			m.errMessage = "Decode autosaved presentation: " + err.Error() + "; press d to discard it"
			return nil
		}
		data.Baseline.Path = m.path
		m.deck = newPresentationModel(m.presentations, m.path, m.theme, m.undoLimit)
		m.deck.recoveryStore = m.recoveryStore
		m.deck.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return presentationAutosaveTickMsg{session: session}
			})
		}
		m.deck.newOperationContext = m.newOperationContext
		m.deck.width, m.deck.height = m.width, m.height
		m.deck.setItem(data.Baseline)
		if len(data.Workbook.Sheets) == 0 && len(data.Baseline.Workbook.Sheets) > 0 {
			data.Workbook = data.Baseline.Workbook
		}
		m.deck.status = "Recovered unsaved presentation draft"
		if !presentation.Equal(data.Baseline, opened.deck) {
			m.deck.status += "; the file changed after the draft was saved, so saving will report a conflict"
		}
		m.stage = stagePresentation
		m.draftStored = true
		_, sizeCmd := m.deck.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		restoreCmd := m.deck.restoreDraft(data.Source, data.Workbook)
		return tea.Batch(sizeCmd, restoreCmd)
	case artifact.SpreadsheetKind:
		var data spreadsheetRecoveryData
		if err := json.Unmarshal(draft.Data, &data); err != nil {
			m.errMessage = "Decode autosaved spreadsheet: " + err.Error() + "; press d to discard it"
			return nil
		}
		data.Baseline.Body, data.Baseline.Blocks = data.BaselineBody, data.BaselineBlocks
		data.Dirty.Body, data.Dirty.Blocks = data.DirtyBody, data.DirtyBlocks
		data.Baseline.Path = m.path
		data.Dirty.Path = m.path
		m.sheet = newSpreadsheetModel(m.spreadsheets, m.path, m.theme, m.undoLimit)
		m.sheet.recoveryStore = m.recoveryStore
		m.sheet.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return spreadsheetAutosaveTickMsg{session: session}
			})
		}
		m.sheet.newOperationContext = m.newOperationContext
		m.sheet.width, m.sheet.height = m.width, m.height
		spreadsheet.Normalize(&data.Dirty)
		m.sheet.setBook(data.Dirty)
		m.sheet.baseline = cloneSpreadsheetState(data.Baseline)
		m.sheet.sheet = min(max(data.ActiveSheet, 0), len(m.sheet.book.Sheets)-1)
		m.sheet.row = data.CursorRow
		m.sheet.column = data.CursorCol
		m.sheet.scrollToSelection()
		if data.Editing || data.PendingCellInput != "" {
			m.sheet.editing = true
			m.sheet.input.SetValue(data.PendingCellInput)
			m.sheet.input.CursorEnd()
		}
		m.sheet.computeValues()
		m.sheet.draftStored = true
		m.sheet.status = "Recovered unsaved spreadsheet draft"
		if !spreadsheet.Equal(data.Baseline, opened.book) {
			m.sheet.status += "; the file changed after the draft was saved, so saving will report a conflict"
		}
		m.stage = stageSpreadsheet
		_, sizeCmd := m.sheet.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		var initCmd tea.Cmd
		if m.sheet.editing {
			initCmd = m.sheet.input.Focus()
		}
		return tea.Batch(sizeCmd, initCmd, m.sheet.syncAutosave())
	}
	return nil
}
