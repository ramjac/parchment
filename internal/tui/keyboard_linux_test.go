package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/unix"

	"example.com/parchment/internal/artifact"
)

type terminalSizeModel struct {
	*Model
}

func (terminalSizeModel) Init() tea.Cmd { return nil }

func (m terminalSizeModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.Model.Update(message)
	if _, ok := message.(tea.WindowSizeMsg); ok {
		return m, tea.Quit
	}
	return m, cmd
}

func TestArtifactEditorsDetectTerminalSizeWithKeyboardReporting(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	go func() {
		_, _ = io.Copy(io.Discard, master)
	}()
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []artifact.Kind{artifact.NoteKind, artifact.DocumentKind, artifact.SpreadsheetKind, artifact.PresentationKind} {
		t.Run(string(kind), func(t *testing.T) {
			h := newHarness(t, kind)
			h.send(tea.WindowSizeMsg{})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			program := tea.NewProgram(terminalSizeModel{h.m},
				tea.WithContext(ctx), tea.WithInput(strings.NewReader("")),
				tea.WithOutput(keyboardTerminalOutput{terminal}),
				tea.WithAltScreen(), tea.WithoutSignalHandler(),
			)
			if _, err := program.Run(); err != nil {
				t.Fatalf("editor did not receive terminal dimensions: %v", err)
			}
			if h.m.width != 100 || h.m.height != 24 || strings.Contains(h.m.View(), "Terminal too small") {
				t.Fatalf("editor dimensions = %dx%d, want 100x24", h.m.width, h.m.height)
			}
		})
	}
}
