package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/recovery"
)

func newRecoveryID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", fmt.Errorf("generate recovery ID: %w", err)
	}
	return hex.EncodeToString(id), nil
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

func (m *Model) stopAutosave() {
	m.autosaveSession++
	if m.autosaveCancel != nil {
		m.autosaveCancel()
		m.autosaveCancel = nil
	}
}

func (m *Model) saveNoteRecovery(session uint64) tea.Cmd {
	if m.recoveryStore == nil {
		return nil
	}
	if !m.dirty() {
		return m.scheduleAutosave(session)
	}
	data := noteRecoveryData{
		Snapshot: m.editingSnapshot, SnapshotBody: m.editingSnapshot.Body,
		SnapshotBlocks: cloneRawMessages(m.editingSnapshot.Blocks),
		Title:          m.titleInput.Value(), Body: m.bodyInput.Value(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.autosaveCancel = cancel
	store := m.recoveryStore
	draft := recovery.Draft{
		ID: m.autosaveID, Kind: "note", Artifact: m.editingID, Created: m.creating,
		Title: data.Title, UpdatedAt: time.Now().UTC(),
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

func (m *Model) loadRecoveries() tea.Cmd {
	store := m.recoveryStore
	return func() tea.Msg {
		drafts, err := store.ListRecovery(context.Background())
		return recoveriesLoadedMsg{drafts: drafts, err: err}
	}
}

func (m *Model) openRecovery() tea.Cmd {
	if m.recoverySelected < 0 || m.recoverySelected >= len(m.recoveries) {
		return nil
	}
	draft := m.recoveries[m.recoverySelected]
	m.recoveryLoading = true
	return func() tea.Msg {
		switch draft.Kind {
		case "note":
			var data noteRecoveryData
			if err := json.Unmarshal(draft.Data, &data); err != nil {
				return recoveryOpenedMsg{draft: draft, err: fmt.Errorf("decode autosaved note: %w", err)}
			}
			if (draft.Created && draft.Artifact != "") ||
				(!draft.Created && (draft.Artifact == "" || data.Snapshot.ID != draft.Artifact || data.Snapshot.Kind != "note")) {
				return recoveryOpenedMsg{draft: draft, err: fmt.Errorf("autosaved note metadata does not match draft %s", draft.ID)}
			}
			return recoveryOpenedMsg{draft: draft, data: data}
		case "document":
			var data documentRecoveryData
			if err := json.Unmarshal(draft.Data, &data); err != nil {
				return documentRecoveryReadyMsg{draft: draft, err: fmt.Errorf("decode autosaved document: %w", err)}
			}
			if (draft.Created && draft.Artifact != "") ||
				(!draft.Created && (draft.Artifact == "" || data.Snapshot.ID != draft.Artifact || data.Snapshot.Kind != "document")) {
				return documentRecoveryReadyMsg{draft: draft, err: fmt.Errorf("autosaved document metadata does not match draft %s", draft.ID)}
			}
			return documentRecoveryReadyMsg{draft: draft, data: data}
		default:
			return recoveryOpenedMsg{draft: draft, err: fmt.Errorf("unsupported autosaved artifact type %q", draft.Kind)}
		}
	}
}

func (m *Model) deleteRecovery() tea.Cmd {
	if m.recoverySelected < 0 || m.recoverySelected >= len(m.recoveries) {
		return nil
	}
	draft := m.recoveries[m.recoverySelected]
	store := m.recoveryStore
	return func() tea.Msg {
		return recoveryDeletedMsg{id: draft.ID, err: store.DeleteRecovery(context.Background(), draft.ID), showStatus: true}
	}
}

func (m *Model) deleteCurrentRecovery() tea.Cmd {
	if m.recoveryStore == nil || m.autosaveID == "" {
		return nil
	}
	store, id := m.recoveryStore, m.autosaveID
	return func() tea.Msg {
		return recoveryDeletedMsg{id: id, err: store.DeleteRecovery(context.Background(), id)}
	}
}

func (m *Model) removeRecovery(id string) {
	for i, draft := range m.recoveries {
		if draft.ID == id {
			m.recoveries = append(m.recoveries[:i], m.recoveries[i+1:]...)
			m.recoverySelected = min(m.recoverySelected, max(len(m.recoveries)-1, 0))
			return
		}
	}
}

func (m Model) recoveryView(header string) string {
	if m.recoveryLoading && len(m.recoveries) == 0 {
		return header + "\n\nChecking for autosaved drafts…"
	}
	var view strings.Builder
	view.WriteString(header + "\n\nAutosaved drafts\n\n")
	if len(m.recoveries) == 0 {
		view.WriteString("No drafts are available.\n\nF6 returns to the workspace")
		view.WriteString(m.statusLine())
		return view.String()
	}
	for i, draft := range m.recoveries {
		marker := "  "
		if i == m.recoverySelected {
			marker = "> "
		}
		title := sanitizeTerminalLine(draft.Title)
		if title == "" {
			title = "Untitled"
		}
		fmt.Fprintf(&view, "%s%s  %s  %s\n", marker, draft.Kind, title, draft.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	view.WriteString("\n↑/↓ select  r recover  d discard  Esc later (F6 to review)")
	view.WriteString(m.statusLine())
	return view.String()
}
