package filerepo_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ramjac/parchment/internal/filerepo"
)

func TestPDFDestinationState(t *testing.T) {
	repo, state, folder := newRepo(t)
	ctx := context.Background()
	if path, err := repo.LastPDFPath(ctx); err != nil || path != "" {
		t.Fatalf("initial PDF path = %q, %v", path, err)
	}
	t.Chdir(folder)
	if err := repo.SavePDFPath(ctx, "report.pdf"); err != nil {
		t.Fatal(err)
	}
	reopened, err := filerepo.New(state, filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if path, err := reopened.LastPDFPath(ctx); err != nil || path != filepath.Join(folder, "report.pdf") {
		t.Fatalf("reopened PDF path = %q, %v", path, err)
	}
	info, err := os.Stat(filepath.Join(state, "last-pdf-path"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("PDF state permissions: %v, %v", info, err)
	}
}

func TestWritePDFExport(t *testing.T) {
	repo, _, folder := newRepo(t)
	ctx := context.Background()
	source := filepath.Join(folder, "source.md")
	target := filepath.Join(folder, "output.pdf")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repo.WritePDFExport(ctx, target, source, []byte("first"), false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := repo.WritePDFExport(ctx, target, source, []byte("second"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("unconfirmed overwrite error = %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "first" {
		t.Fatalf("unconfirmed output = %q, %v", data, err)
	}
	if err := repo.WritePDFExport(ctx, target, source, []byte("second"), true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "second" {
		t.Fatalf("confirmed output = %q, %v", data, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("overwritten output permissions = %v, %v", info, err)
	}

	if err := repo.WritePDFExport(ctx, source, source, []byte("bad"), true); err == nil {
		t.Fatal("source document overwrite succeeded")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(folder, "source-link.pdf")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		if err := repo.WritePDFExport(ctx, link, source, []byte("bad"), true); err == nil {
			t.Fatal("source symlink overwrite succeeded")
		}
		hardlink := filepath.Join(folder, "source-hardlink.pdf")
		if err := os.Link(source, hardlink); err != nil {
			t.Fatal(err)
		}
		if err := repo.WritePDFExport(ctx, hardlink, source, []byte("bad"), true); err == nil {
			t.Fatal("source hardlink overwrite succeeded")
		}
		outputLink := filepath.Join(folder, "output-link.pdf")
		if err := os.Symlink(target, outputLink); err != nil {
			t.Fatal(err)
		}
		if err := repo.WritePDFExport(ctx, outputLink, source, []byte("linked"), true); err != nil {
			t.Fatal(err)
		}
		linkInfo, err := os.Lstat(outputLink)
		if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("output link was replaced: %v", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "linked" {
			t.Fatalf("output link target = %q, %v", data, err)
		}
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "source" {
		t.Fatalf("source document = %q, %v", data, err)
	}
}
