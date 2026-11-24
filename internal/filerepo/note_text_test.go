package filerepo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramjac/parchment/internal/note"
	"github.com/ramjac/parchment/internal/notetext"
)

func TestPlainTextSessionsPreserveBytesAndHistory(t *testing.T) {
	for _, format := range []notetext.Format{
		{Encoding: notetext.UTF8, BOM: true},
		{Encoding: notetext.UTF16LE, BOM: true},
		{Encoding: notetext.UTF16BE},
		{Encoding: notetext.Windows1252},
		{Encoding: notetext.Latin1},
		{Encoding: notetext.ShiftJIS},
	} {
		t.Run(format.Encoding, func(t *testing.T) {
			ctx := context.Background()
			state := t.TempDir()
			repo, _ := New(state, Options{})
			service := note.NewService(repo, 10)
			path := filepath.Join(t.TempDir(), "plain.txt")
			data, err := notetext.Encode(notetext.Split("one\r\ntwo\rthree\n"), format)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := service.OpenText(ctx, path, format.Encoding)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveTextExpected(ctx, snapshot, snapshot.Document); err != nil {
				t.Fatal(err)
			}
			if service.CanUndo() {
				t.Fatal("no-op save created history")
			}
			edited := snapshot.Document.Clone()
			edited.Text = "changed\r" // invalid logical control must not reach history
			if _, err := service.SaveTextExpected(ctx, snapshot, edited); err == nil {
				t.Fatal("saved invalid logical text")
			}
			edited = snapshot.Document.Clone()
			edited.Text = "changed\n" + snapshot.Document.Text
			edited.Endings = append([]notetext.Ending{notetext.CRLF}, edited.Endings...)
			saved, err := service.SaveTextExpected(ctx, snapshot, edited)
			if err != nil {
				t.Fatal(err)
			}
			reopenedRepo, _ := New(state, Options{})
			reopened := note.NewService(reopenedRepo, 10)
			if _, err := reopened.OpenText(ctx, path, format.Encoding); err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.Undo(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, data) {
				t.Fatal("undo did not restore exact source")
			}
			if _, err := reopened.Redo(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ = os.ReadFile(path)
			if !bytes.Equal(got, saved.Raw) {
				t.Fatal("redo did not restore exact save")
			}
			external := append(append([]byte(nil), got...), 'x')
			if err := os.WriteFile(path, external, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveTextExpected(ctx, saved, snapshot.Document); err == nil {
				t.Fatal("overwrote external bytes")
			}
		})
	}
}

func TestPlainTextSessionRejectsEncodedArtifactAndLossySave(t *testing.T) {
	ctx := context.Background()
	repo, _ := New(t.TempDir(), Options{})
	service := note.NewService(repo, 10)
	path := filepath.Join(t.TempDir(), "note.md")
	if _, err := service.Create(ctx, path, "body"); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	if _, err := service.OpenText(ctx, path, notetext.Latin1); err == nil {
		t.Fatal("encoding override accepted for typed note")
	}
	withBOM := append([]byte{0xef, 0xbb, 0xbf}, original...)
	if err := os.WriteFile(path, withBOM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenText(ctx, path, notetext.Auto); err == nil {
		t.Fatal("encoded artifact treated as plain")
	}
	if err := os.WriteFile(path, []byte("caf\xe9"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.OpenText(ctx, path, notetext.Windows1252)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveTextExpected(ctx, snapshot, notetext.Split("emoji 😀")); err == nil {
		t.Fatal("lossy save accepted")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, snapshot.Raw) || service.CanUndo() {
		t.Fatal("failed save changed file or history")
	}
}
