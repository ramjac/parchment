//go:build linux

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestScreenReadyAndErase(t *testing.T) {
	s := newScreen()
	s.feed([]byte("\x1b[4;29HThe Project Gutenberg eBook"))
	s.feed([]byte("\x1b[5;29HTitle: War and Peace"))
	for row := 6; row <= 17; row++ {
		s.feed([]byte(fmt.Sprintf("\x1b[%d;29Htext", row)))
	}
	if !s.ready() {
		t.Fatal("expected a full screen with both markers")
	}
	s.feed([]byte("\x1b[5;29H\x1b[K"))
	if s.ready() {
		t.Fatal("erased title must not be visible")
	}
}

func TestScreenSplitSequencesAndScrolling(t *testing.T) {
	s := newScreen()
	s.feed([]byte("\x1b[2;"))
	s.feed([]byte("30H\xc3"))
	s.feed([]byte("\xa9"))
	if s.cells[1][29] != 'é' {
		t.Fatalf("split cursor and UTF-8 sequence: got %q", s.cells[1][29])
	}
	s.feed([]byte("\x1b[24;1H" + strings.Repeat("\n", 2)))
	if s.cells[0][29] != 0 {
		t.Fatal("content scrolled out should not remain visible")
	}
}
