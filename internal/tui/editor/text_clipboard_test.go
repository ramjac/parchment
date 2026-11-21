package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"example.com/parchment/internal/notetext"
)

func TestPlainTextClipboardPasteUsesLocalEndings(t *testing.T) {
	unsupported, previous := clipboard.Unsupported, internalClipboard
	clipboard.Unsupported, internalClipboard = true, "a\r\nb\rc"
	t.Cleanup(func() { clipboard.Unsupported, internalClipboard = unsupported, previous })
	m := New()
	m.Focus()
	if err := m.SetDocument(notetext.Split("x\r")); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	source, err := m.Document().Source()
	if err != nil || source != "a\rb\rcx\r" {
		t.Fatalf("clipboard paste = %q, %v", source, err)
	}
}
