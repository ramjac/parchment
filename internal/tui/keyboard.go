package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type keyboardOutput struct {
	io.Writer
}

type keyboardTerminalOutput struct {
	*os.File
}

func (w keyboardTerminalOutput) Write(p []byte) (int, error) {
	return (keyboardOutput{w.File}).Write(p)
}

func (w keyboardTerminalOutput) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w keyboardOutput) Write(p []byte) (int, error) {
	// Keyboard modes belong to the alternate screen: enable after entering and
	// restore before leaving, including Bubble Tea's error and signal cleanup.
	enable := ansi.SetModifyOtherKeys1 + ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes)
	disable := ansi.PopKittyKeyboard(1) + ansi.ResetModifyOtherKeys
	data := bytes.ReplaceAll(p, []byte(ansi.SetAltScreenSaveCursorMode), []byte(ansi.SetAltScreenSaveCursorMode+enable))
	data = bytes.ReplaceAll(data, []byte(ansi.ResetAltScreenSaveCursorMode), []byte(disable+ansi.ResetAltScreenSaveCursorMode))
	n, err := w.Writer.Write(data)
	if err != nil {
		return 0, err
	}
	if n != len(data) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

func normalizeTerminalKey(message tea.Msg) tea.Msg {
	// Bubble Tea v1 exposes CSI-u and modifyOtherKeys input as unnamed messages
	// whose String method contains the sequence's decimal bytes.
	msg, ok := message.(fmt.Stringer)
	if !ok {
		return message
	}
	text := msg.String()
	if !strings.HasPrefix(text, "?CSI[") || !strings.HasSuffix(text, "]?") {
		return message
	}
	var sequence strings.Builder
	for _, field := range strings.Fields(text[len("?CSI[") : len(text)-len("]?")]) {
		b, err := strconv.ParseUint(field, 10, 8)
		if err != nil {
			return message
		}
		sequence.WriteByte(byte(b))
	}
	raw := sequence.String()
	var fields []string
	switch {
	case strings.HasSuffix(raw, "u"):
		fields = strings.Split(strings.TrimSuffix(raw, "u"), ";")
	case strings.HasPrefix(raw, "27;") && strings.HasSuffix(raw, "~"):
		parts := strings.Split(strings.TrimSuffix(raw, "~"), ";")
		if len(parts) != 3 {
			return message
		}
		fields = []string{parts[2], parts[1]}
	default:
		return message
	}
	if len(fields) < 1 || len(fields) > 2 {
		return message
	}
	code, err := strconv.Atoi(fields[0])
	if err != nil || code < 0 || code > utf8.MaxRune {
		return message
	}
	modifiers := 1
	if len(fields) == 2 {
		modifiers, err = strconv.Atoi(fields[1])
		if err != nil || modifiers < 1 {
			return message
		}
	}
	// Caps Lock and Num Lock do not change shortcut matching.
	mods := (modifiers - 1) &^ (64 | 128)
	if mods & ^7 != 0 {
		return message
	}
	if code == 13 && mods == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")}
	}
	key := tea.KeyMsg{Alt: mods&2 != 0}
	if mods&4 != 0 {
		switch {
		case code >= 'a' && code <= 'z':
			code -= 'a' - 1
		case code >= '@' && code <= '_':
			code -= '@'
		case code == ' ':
			code = 0
		default:
			return message
		}
	}
	switch {
	case code == 9 && mods&1 != 0:
		key.Type = tea.KeyShiftTab
	case code <= 31 || code == 127:
		key.Type = tea.KeyType(code)
	case code == ' ':
		key.Type = tea.KeySpace
	default:
		key.Type, key.Runes = tea.KeyRunes, []rune{rune(code)}
	}
	return key
}
