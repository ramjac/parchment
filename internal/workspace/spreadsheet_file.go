package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/spreadsheet"
)

// SpreadsheetFile stores one existing workbook directly, without a workspace.
type SpreadsheetFile struct {
	path string
	mu   sync.Mutex
	book spreadsheet.Spreadsheet
	data []byte
}

// OpenSpreadsheetFile opens an existing standalone spreadsheet artifact.
func OpenSpreadsheetFile(path string) (*SpreadsheetFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve spreadsheet path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve spreadsheet file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect spreadsheet file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("spreadsheet file %s is not a regular file", resolved)
	}
	if info.Size() > artifactfile.MaxFileSize {
		return nil, fmt.Errorf("spreadsheet file is larger than %d MiB", artifactfile.MaxFileSize>>20)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read spreadsheet file: %w", err)
	}
	book, err := spreadsheet.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("open spreadsheet file: %w", err)
	}
	book.ID = standaloneID(book.Kind, resolved)
	book.Title = standaloneTitle(resolved)
	book.Location = filepath.ToSlash(resolved)
	return &SpreadsheetFile{path: resolved, book: book, data: data}, nil
}

func (f *SpreadsheetFile) ArtifactLocation(id string) string {
	if id != f.book.ID {
		return ""
	}
	return f.book.Location
}

func (f *SpreadsheetFile) ListSpreadsheets(ctx context.Context) ([]spreadsheet.Spreadsheet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []spreadsheet.Spreadsheet{f.book}, nil
}

func (f *SpreadsheetFile) GetSpreadsheet(ctx context.Context, id string) (spreadsheet.Spreadsheet, error) {
	if err := ctx.Err(); err != nil {
		return spreadsheet.Spreadsheet{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.book.ID {
		return spreadsheet.Spreadsheet{}, spreadsheet.ErrNotFound
	}
	return f.book, nil
}

func (f *SpreadsheetFile) TransitionSpreadsheet(ctx context.Context, id string, expected, target *spreadsheet.Spreadsheet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.book.ID {
		return spreadsheet.ErrNotFound
	}
	if expected == nil || target == nil {
		return errors.New("cannot create or delete a standalone spreadsheet")
	}
	if !spreadsheet.Equal(f.book, *expected) {
		return errors.New("stale spreadsheet file")
	}
	if target.ID != id || target.Kind != f.book.Kind || target.Location != f.book.Location {
		return errors.New("invalid standalone spreadsheet update")
	}
	info, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("inspect spreadsheet before save: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("spreadsheet file is not a regular file")
	}
	current, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read spreadsheet before save: %w", err)
	}
	if !bytes.Equal(current, f.data) {
		return errors.New("spreadsheet file changed outside Parchment")
	}
	data, err := spreadsheet.Encode(*target)
	if err != nil {
		return err
	}
	if err := writeAtomic(f.path, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("save spreadsheet file: %w", err)
	}
	f.book = *target
	f.data = data
	return nil
}
