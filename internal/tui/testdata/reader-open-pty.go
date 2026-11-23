//go:build linux

// reader-open-pty measures novel-reader startup and sampled peak RSS on Linux.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

const (
	width  = 80
	height = 24
)

// screen reconstructs the terminal cells needed by the first-screen check.
type screen struct {
	cells  [height][width]rune
	row    int
	col    int
	savedR int
	savedC int
	parser *ansi.Parser
}

func newScreen() *screen {
	s := &screen{parser: ansi.NewParser()}
	s.parser.SetHandler(ansi.Handler{
		Print: s.print,
		Execute: func(b byte) {
			switch b {
			case '\r':
				s.col = 0
			case '\n', '\v', '\f':
				s.lineFeed()
			case '\b':
				s.col = max(0, s.col-1)
			case '\t':
				s.col = min(width-1, (s.col/8+1)*8)
			}
		},
		HandleCsi: s.csi,
		HandleEsc: func(cmd ansi.Cmd) {
			switch cmd.Final() {
			case '7':
				s.savedR, s.savedC = s.row, s.col
			case '8':
				s.row, s.col = s.savedR, s.savedC
			case 'D':
				s.lineFeed()
			case 'E':
				s.col = 0
				s.lineFeed()
			}
		},
	})
	return s
}

func (s *screen) print(r rune) {
	w := runewidth.RuneWidth(r)
	if w <= 0 {
		return
	}
	if s.col >= width || s.col+w > width {
		s.col = 0
		s.lineFeed()
	}
	s.cells[s.row][s.col] = r
	for i := 1; i < w; i++ {
		s.cells[s.row][s.col+i] = ' '
	}
	s.col += w
}

func (s *screen) lineFeed() {
	if s.row < height-1 {
		s.row++
		return
	}
	copy(s.cells[:], s.cells[1:])
	s.cells[height-1] = [width]rune{}
}

func (s *screen) csi(cmd ansi.Cmd, params ansi.Params) {
	p := func(i, fallback int) int {
		n, _, ok := params.Param(i, fallback)
		if !ok || n == 0 {
			return fallback
		}
		return n
	}
	switch cmd.Final() {
	case 'H', 'f':
		s.row, s.col = min(height-1, p(0, 1)-1), min(width-1, p(1, 1)-1)
	case 'A':
		s.row = max(0, s.row-p(0, 1))
	case 'B', 'e':
		s.row = min(height-1, s.row+p(0, 1))
	case 'C', 'a':
		s.col = min(width-1, s.col+p(0, 1))
	case 'D':
		s.col = max(0, s.col-p(0, 1))
	case 'E':
		s.row, s.col = min(height-1, s.row+p(0, 1)), 0
	case 'F':
		s.row, s.col = max(0, s.row-p(0, 1)), 0
	case 'G', '`':
		s.col = min(width-1, p(0, 1)-1)
	case 'd':
		s.row = min(height-1, p(0, 1)-1)
	case 'J':
		mode, _, _ := params.Param(0, 0)
		for r := range height {
			for c := range width {
				if mode == 2 || mode == 3 || (mode == 0 && (r > s.row || r == s.row && c >= s.col)) || (mode == 1 && (r < s.row || r == s.row && c <= s.col)) {
					s.cells[r][c] = 0
				}
			}
		}
	case 'K':
		mode, _, _ := params.Param(0, 0)
		for c := range width {
			if mode == 2 || (mode == 0 && c >= s.col) || (mode == 1 && c <= s.col) {
				s.cells[s.row][c] = 0
			}
		}
	case 's':
		s.savedR, s.savedC = s.row, s.col
	case 'u':
		s.row, s.col = s.savedR, s.savedC
	}
}

func (s *screen) feed(data []byte) {
	for _, b := range data {
		s.parser.Advance(b)
	}
}

func (s *screen) ready() bool {
	opening, title, nonblank := false, false, 0
	for r := 3; r < 22; r++ {
		row := strings.TrimSpace(strings.ReplaceAll(string(s.cells[r][28:78]), "\x00", " "))
		opening = opening || strings.Contains(row, "The Project Gutenberg eBook")
		title = title || strings.Contains(row, "Title: War and Peace")
		if row != "" {
			nonblank++
		}
	}
	return opening && title && nonblank >= 14
}

func openPTY() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	defer func() {
		if err != nil {
			master.Close()
		}
	}()
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return nil, nil, err
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		return nil, nil, err
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err = unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: height, Col: width}); err != nil {
		slave.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func peakRSS(pid int) int {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "VmHWM:" {
			kib, _ := strconv.Atoi(fields[1])
			return kib
		}
	}
	return 0
}

type result struct {
	first, end, home time.Duration
	peak             int
}

func measure(binary, document, home, mode string) (result, error) {
	var result result
	master, slave, err := openPTY()
	if err != nil {
		return result, err
	}
	defer master.Close()
	command := exec.Command(binary, document)
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "HOME", "TERM", "GOMEMLIMIT", "KITTY_WINDOW_ID", "TERM_PROGRAM", "LC_TERMINAL":
		default:
			command.Env = append(command.Env, env)
		}
	}
	command.Env = append(command.Env, "HOME="+home, "TERM=xterm-256color", "GOMEMLIMIT=512MiB")
	started := time.Now()
	err = command.Start()
	slave.Close()
	if err != nil {
		return result, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			<-done
		}
	}()
	chunks := make(chan []byte, 1)
	stopReading := make(chan struct{})
	defer close(stopReading)
	go func() {
		defer close(chunks)
		buf := make([]byte, 65536)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-stopReading:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()
	navigate := mode == "--end" || mode == "--roundtrip"
	var display *screen
	if mode == "--screen" {
		display = newScreen()
	}
	var tail []byte
	var firstSeen, endSeen, homeSeen, quitting bool
	quit := func(key string) error {
		_, err := io.WriteString(master, key)
		return err
	}
	for {
		select {
		case data, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			tail = append(tail, data...)
			if display != nil {
				display.feed(data)
			}
			marker := []byte("Gutenberg")
			if mode == "--full" {
				marker = []byte("Title:")
			}
			ready := bytes.Contains(tail, marker)
			if display != nil {
				ready = display.ready()
			}
			if !firstSeen && ready {
				firstSeen = true
				result.first = time.Since(started)
				key := "q"
				if navigate {
					key = "G"
				} else {
					quitting = true
					timeout.Reset(3 * time.Second)
				}
				if err := quit(key); err != nil {
					return result, err
				}
			}
			if navigate && firstSeen && !endSeen && bytes.Contains(tail, []byte("subscribe")) {
				endSeen = true
				result.end = time.Since(started)
				key := "q"
				if mode == "--roundtrip" {
					key = "g"
					tail = nil
				} else {
					quitting = true
					timeout.Reset(3 * time.Second)
				}
				if err := quit(key); err != nil {
					return result, err
				}
			} else if mode == "--roundtrip" && endSeen && !homeSeen && bytes.Contains(tail, []byte("Gutenberg")) {
				homeSeen = true
				result.home = time.Since(started)
				quitting = true
				timeout.Reset(3 * time.Second)
				if err := quit("q"); err != nil {
					return result, err
				}
			}
			if len(tail) > 1024 {
				tail = append([]byte(nil), tail[len(tail)-1024:]...)
			}
		case <-ticker.C:
			result.peak = max(result.peak, peakRSS(command.Process.Pid))
		case err := <-done:
			waited = true
			if err != nil || !firstSeen || (navigate && !endSeen) || (mode == "--roundtrip" && !homeSeen) {
				return result, fmt.Errorf("reader failed: exit=%v, tail=%q", err, tail)
			}
			return result, nil
		case <-timeout.C:
			if quitting {
				_ = command.Process.Signal(syscall.SIGTERM)
			}
			return result, fmt.Errorf("reader timed out: tail=%q", tail)
		}
	}
}

func run(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New("usage: go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment /absolute/path/war-and-peace.md [--end|--full|--roundtrip|--screen]")
	}
	mode := ""
	if len(args) == 3 {
		mode = args[2]
		if mode != "--end" && mode != "--full" && mode != "--roundtrip" && mode != "--screen" {
			return errors.New("unknown mode: " + mode)
		}
	}
	binary, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	document, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp("", "parchment-reader-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	count := 3
	if mode == "--end" || mode == "--roundtrip" {
		count = 1
	}
	for i := 1; i <= count; i++ {
		res, err := measure(binary, document, home, mode)
		if err != nil {
			return err
		}
		label := "first_content_s"
		if mode == "--full" {
			label = "first_viewport_s"
		} else if mode == "--screen" {
			label = "first_screen_s"
		}
		fmt.Printf("run=%d %s=%.4f", i, label, res.first.Seconds())
		if mode == "--end" || mode == "--roundtrip" {
			fmt.Printf(" end_content_s=%.4f", res.end.Seconds())
		}
		if mode == "--roundtrip" {
			fmt.Printf(" home_content_s=%.4f", res.home.Seconds())
		}
		fmt.Printf(" peak_kib=%d\n", res.peak)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
