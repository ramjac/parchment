package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type keyboardOutput struct {
	io.Writer
}

type keyboardTerminalOutput struct {
	*os.File
}

type shiftEnterMsg struct{}

const protectedITermPrefix = "\x1b_PARCHMENT-OSC:"
const protectedITermSuffix = "\x1b\\"
const imageRefreshPrefix = "\x1b_PARCHMENT-REFRESH:"

type mouseAction uint8

const (
	mousePress mouseAction = iota
	mouseMotion
	mouseRelease
)

type mouseButton uint8

const (
	mouseNone mouseButton = iota
	mouseLeft
	mouseRight
	mouseMiddle
	mouseWheelUp
	mouseWheelDown
	mouseWheelLeft
	mouseWheelRight
)

type mouseEvent struct {
	X, Y   int
	Action mouseAction
	Button mouseButton
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
	data, err := restoreITermGraphics(p)
	if err != nil {
		return 0, err
	}
	data = bytes.ReplaceAll(data, []byte(ansi.SetAltScreenSaveCursorMode), []byte(ansi.SetAltScreenSaveCursorMode+enable))
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

func protectITermGraphics(content string) string {
	const prefix = "\x1b]1337;File="
	for {
		start := strings.Index(content, prefix)
		if start < 0 {
			return content
		}
		end := strings.IndexByte(content[start:], '\a')
		if end < 0 {
			return content
		}
		end += start + 1
		encoded := base64.RawStdEncoding.EncodeToString([]byte(content[start:end]))
		content = content[:start] + protectedITermPrefix + encoded + protectedITermSuffix + content[end:]
	}
}

func restoreITermGraphics(data []byte) ([]byte, error) {
	var out bytes.Buffer
	for len(data) > 0 {
		imageStart := bytes.Index(data, []byte(protectedITermPrefix))
		refreshStart := bytes.Index(data, []byte(imageRefreshPrefix))
		if imageStart < 0 && refreshStart < 0 {
			out.Write(data)
			break
		}
		start := imageStart
		if start < 0 || (refreshStart >= 0 && refreshStart < start) {
			start = refreshStart
		}
		out.Write(data[:start])
		if start == refreshStart {
			data = data[start+len(imageRefreshPrefix):]
			end := bytes.Index(data, []byte(protectedITermSuffix))
			if end < 0 {
				return nil, errors.New("incomplete image refresh marker")
			}
			data = data[end+len(protectedITermSuffix):]
			continue
		}
		data = data[start+len(protectedITermPrefix):]
		end := bytes.Index(data, []byte(protectedITermSuffix))
		if end < 0 {
			return nil, errors.New("incomplete protected iTerm2 image sequence")
		}
		sequence, err := base64.RawStdEncoding.DecodeString(string(data[:end]))
		if err != nil || !bytes.HasPrefix(sequence, []byte("\x1b]1337;File=")) || sequence[len(sequence)-1] != '\a' {
			return nil, errors.New("invalid protected iTerm2 image sequence")
		}
		out.WriteString("\x1b[s")
		out.Write(sequence)
		out.WriteString("\x1b[u")
		data = data[end+len(protectedITermSuffix):]
	}
	return out.Bytes(), nil
}

func normalizeTerminalKey(message tea.Msg) tea.Msg {
	switch msg := message.(type) {
	case tea.MouseClickMsg:
		return normalizeMouse(msg.Mouse(), mousePress)
	case tea.MouseReleaseMsg:
		return normalizeMouse(msg.Mouse(), mouseRelease)
	case tea.MouseMotionMsg:
		return normalizeMouse(msg.Mouse(), mouseMotion)
	case tea.MouseWheelMsg:
		return normalizeMouse(msg.Mouse(), mousePress)
	}

	// Some terminals expose CSI-u and modifyOtherKeys input as unnamed
	// messages whose String method contains the sequence's decimal bytes.
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
		return shiftEnterMsg{}
	}
	key := tea.KeyPressMsg{Code: rune(code)}
	if mods&1 != 0 {
		key.Mod |= tea.ModShift
	}
	if mods&2 != 0 {
		key.Mod |= tea.ModAlt
	}
	if mods&4 != 0 {
		if code >= 'A' && code <= 'Z' {
			code += 'a' - 'A'
		}
		if code >= 'a' && code <= 'z' {
			key.Code = rune(code)
			key.Mod |= tea.ModCtrl
			return key
		}
		return message
	}
	switch {
	case code == 9 && mods&1 != 0:
		key.Code = tea.KeyTab
	case code == ' ':
		key.Code, key.Text = tea.KeySpace, " "
	case code == 13:
		key.Code = tea.KeyEnter
	case code == 27:
		key.Code = tea.KeyEscape
	case code == 127:
		key.Code = tea.KeyBackspace
	case code > 31 && code < utf8.MaxRune:
		key.Text = string(rune(code))
	case mods&1 != 0 && code >= 'A' && code <= 'Z':
		key.Code, key.Text = rune(code+'a'-'A'), string(rune(code))
	}
	return key
}

func normalizeMouse(mouse tea.Mouse, action mouseAction) mouseEvent {
	button := mouseNone
	switch mouse.Button {
	case tea.MouseLeft:
		button = mouseLeft
	case tea.MouseRight:
		button = mouseRight
	case tea.MouseMiddle:
		button = mouseMiddle
	case tea.MouseWheelUp:
		button = mouseWheelUp
	case tea.MouseWheelDown:
		button = mouseWheelDown
	case tea.MouseWheelLeft:
		button = mouseWheelLeft
	case tea.MouseWheelRight:
		button = mouseWheelRight
	}
	return mouseEvent{X: mouse.X, Y: mouse.Y, Action: action, Button: button}
}

func toTeaMouseMessage(event mouseEvent) tea.MouseMsg {
	button := tea.MouseNone
	switch event.Button {
	case mouseLeft:
		button = tea.MouseLeft
	case mouseRight:
		button = tea.MouseRight
	case mouseMiddle:
		button = tea.MouseMiddle
	case mouseWheelUp:
		button = tea.MouseWheelUp
	case mouseWheelDown:
		button = tea.MouseWheelDown
	case mouseWheelLeft:
		button = tea.MouseWheelLeft
	case mouseWheelRight:
		button = tea.MouseWheelRight
	}
	mouse := tea.Mouse{X: event.X, Y: event.Y, Button: button}
	if button == tea.MouseWheelUp || button == tea.MouseWheelDown ||
		button == tea.MouseWheelLeft || button == tea.MouseWheelRight {
		return tea.MouseWheelMsg(mouse)
	}
	switch event.Action {
	case mouseMotion:
		return tea.MouseMotionMsg(mouse)
	case mouseRelease:
		return tea.MouseReleaseMsg(mouse)
	default:
		return tea.MouseClickMsg(mouse)
	}
}
