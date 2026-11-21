package editor

import (
	"errors"
	"slices"
	"strings"

	"example.com/parchment/internal/notetext"
)

func (m *Model) allowInsertion(text string) bool {
	if err := notetext.ValidateText(text); err != nil {
		m.inputErr = err
		return false
	}
	removed := m.SelectedText()
	if len(m.Value())-len(removed)+len(text) > notetext.MaxText ||
		len(m.Lines)-strings.Count(removed, "\n")+strings.Count(text, "\n") > notetext.MaxLines {
		m.inputErr = errors.New("note exceeds editor limits (1,000,000 UTF-8 bytes or 10,000 lines)")
		return false
	}
	return true
}

func (m *Model) SetDocument(doc notetext.Document) error {
	if _, err := doc.Source(); err != nil {
		return err
	}
	m.SetValue(doc.Text)
	m.preserveEndings = true
	m.endings = slices.Clone(doc.Endings)
	return nil
}

func (m Model) Document() notetext.Document {
	return notetext.Document{Text: m.Value(), Endings: slices.Clone(m.endings)}
}

func (m *Model) TakeInputError() error {
	err := m.inputErr
	m.inputErr = nil
	return err
}

func (m Model) nearestEnding() notetext.Ending {
	line := m.Cursor.Line
	hasPrevious := line > 0 && line-1 < len(m.endings)
	hasNext := line < len(m.endings)
	if hasPrevious && (!hasNext || m.Cursor.Col+1 <= len(m.Lines[line])-m.Cursor.Col) {
		return m.endings[line-1]
	}
	if hasNext {
		return m.endings[line]
	}
	return notetext.LF
}
