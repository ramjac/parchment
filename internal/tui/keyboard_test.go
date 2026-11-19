package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestKeyboardTerminalOutputHandlesStringWrites(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	output := keyboardTerminalOutput{file}
	input := ansi.SetAltScreenSaveCursorMode + "spreadsheet" + ansi.ResetAltScreenSaveCursorMode
	n, err := io.WriteString(output, input)
	if err != nil || n != len(input) {
		t.Fatalf("write terminal controls: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := ansi.SetAltScreenSaveCursorMode +
		ansi.SetModifyOtherKeys1 + ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes) +
		"spreadsheet" +
		ansi.PopKittyKeyboard(1) + ansi.ResetModifyOtherKeys +
		ansi.ResetAltScreenSaveCursorMode
	if string(got) != want {
		t.Fatalf("terminal controls = %q, want %q", got, want)
	}
}

type keyboardTestTerminal struct {
	bytes.Buffer
	input   *io.PipeWriter
	started bool
	xterm   bool
}

func (t *keyboardTestTerminal) Write(p []byte) (int, error) {
	n, err := t.Buffer.Write(p)
	if !t.started && bytes.Contains(p, []byte(ansi.SetAltScreenSaveCursorMode)) {
		t.started = true
		enter := "\r"
		quit := "\x1b[99;5u"
		if t.xterm && bytes.Contains(p, []byte(ansi.SetModifyOtherKeys1)) {
			enter = "\x1b[27;2;13~"
			quit = "\x03"
		} else if !t.xterm && bytes.Contains(p, []byte(ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes))) {
			enter = "\x1b[13;2u"
		}
		go func() {
			_, _ = io.WriteString(t.input, enter+quit)
		}()
	}
	return n, err
}

func TestSpreadsheetEnablesModifiedKeysAndRestoresTerminal(t *testing.T) {
	for _, protocol := range []string{"kitty", "xterm"} {
		t.Run(protocol, func(t *testing.T) {
			m, _, _, _ := newSheetModel(t, nil)
			driveSheet(t, m, sheetKey("enter"))
			m.input.SetValue("column data")
			input, terminalInput := io.Pipe()
			defer input.Close()
			defer terminalInput.Close()
			terminal := &keyboardTestTerminal{input: terminalInput, xterm: protocol == "xterm"}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			program := tea.NewProgram(loadedSpreadsheetInputModel{m},
				tea.WithContext(ctx), tea.WithInput(input),
				tea.WithOutput(keyboardOutput{terminal}),
				tea.WithAltScreen(), tea.WithoutSignalHandler(),
			)
			if _, err := program.Run(); err != nil {
				t.Fatal(err)
			}
			if !m.editing || m.row != 1 || m.column != 0 || m.cell(0, 0).Value != "column data" {
				t.Fatalf("negotiated shortcut: row=%d column=%d editing=%t cell=%#v", m.row, m.column, m.editing, m.cell(0, 0))
			}
			output := terminal.String()
			enabled := ansi.SetAltScreenSaveCursorMode + ansi.SetModifyOtherKeys1 + ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes)
			disabled := ansi.PopKittyKeyboard(1) + ansi.ResetModifyOtherKeys + ansi.ResetAltScreenSaveCursorMode
			if !strings.Contains(output, enabled) || !strings.Contains(output, disabled) {
				t.Fatalf("missing keyboard setup or cleanup: %q", output)
			}
		})
	}
}

type keyboardCaptureModel struct {
	keys []string
}

func (*keyboardCaptureModel) Init() tea.Cmd { return nil }
func (*keyboardCaptureModel) View() string  { return "" }
func (m *keyboardCaptureModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch key := normalizeTerminalKey(message).(type) {
	case shiftEnterMsg:
		m.keys = append(m.keys, "shift+enter")
	case tea.KeyMsg:
		m.keys = append(m.keys, key.String())
		if key.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func TestModifiedKeyboardPreservesExistingKeys(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"kitty", "\x1b[13;2u\x1b[115;5u\x1b[122;5u\x1b[114;5u\x1b[122;6u\x1b[27u\r\t\x1b[9;2u\x1b[13;3u\x1b[99;5u", "shift+enter,ctrl+s,ctrl+z,ctrl+r,ctrl+shift+z,esc,enter,tab,shift+tab,alt+enter,ctrl+c"},
		{"xterm", "\x1b[27;2;13~\x1b[27;5;115~\x1b[27;6;122~\x1b[27;5;99~", "shift+enter,ctrl+s,ctrl+shift+z,ctrl+c"},
		{"legacy", "\r\x1b\r\x13\x03", "enter,alt+enter,ctrl+s,ctrl+c"},
		{"lock modifiers", "\x1b[13;66u\x1b[13;130u\x1b[99;197u", "shift+enter,shift+enter,ctrl+c"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := &keyboardCaptureModel{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			program := tea.NewProgram(m, tea.WithContext(ctx),
				tea.WithInput(strings.NewReader(test.input)), tea.WithOutput(io.Discard),
				tea.WithoutRenderer(), tea.WithoutSignalHandler(),
			)
			if _, err := program.Run(); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(m.keys, ","); got != test.want {
				t.Fatalf("decoded keys = %q, want %q", got, test.want)
			}
		})
	}
}
