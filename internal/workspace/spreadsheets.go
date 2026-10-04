package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/spreadsheet"
)

const spreadsheetName = "spreadsheet.json"

// ListSpreadsheets implements the spreadsheet repository interface.
func (w *Workspace) ListSpreadsheets(ctx context.Context) ([]spreadsheet.Spreadsheet, error) {
	root := filepath.Join(w.root, ".parchment", "artifacts")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var books []spreadsheet.Spreadsheet
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		var book spreadsheet.Spreadsheet
		err := withArtifactLock(ctx, root, entry.Name(), func() error {
			var readErr error
			book, readErr = w.readSpreadsheetUnlocked(entry.Name())
			return readErr
		})
		if errors.Is(err, spreadsheet.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		books = append(books, book)
	}
	return books, nil
}

// GetSpreadsheet returns a workbook by stable artifact ID.
func (w *Workspace) GetSpreadsheet(ctx context.Context, id string) (spreadsheet.Spreadsheet, error) {
	if err := ctx.Err(); err != nil {
		return spreadsheet.Spreadsheet{}, err
	}
	if !validID.MatchString(id) {
		return spreadsheet.Spreadsheet{}, spreadsheet.ErrNotFound
	}
	var book spreadsheet.Spreadsheet
	err := withArtifactLock(ctx, filepath.Join(w.root, ".parchment", "artifacts"), id, func() error {
		var readErr error
		book, readErr = w.readSpreadsheetUnlocked(id)
		return readErr
	})
	return book, err
}

// TransitionSpreadsheet applies a compare-and-swap update to one workbook.
func (w *Workspace) TransitionSpreadsheet(
	ctx context.Context, id string, expected, target *spreadsheet.Spreadsheet,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return spreadsheet.ErrNotFound
	}
	if expected != nil && expected.ID != id {
		return errors.New("expected spreadsheet ID does not match transition ID")
	}
	if target != nil && target.ID != id {
		return errors.New("target spreadsheet ID does not match transition ID")
	}
	root := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, root, id, func() error {
		dir := filepath.Join(root, id)
		_, dirErr := os.Lstat(dir)
		if errors.Is(dirErr, os.ErrNotExist) {
			if expected == nil && target != nil {
				return w.saveSpreadsheetLocked(ctx, *target)
			}
			return spreadsheet.ErrNotFound
		}
		if dirErr != nil {
			return fmt.Errorf("inspect artifact storage: %w", dirErr)
		}
		current, err := w.readSpreadsheetUnlocked(id)
		if errors.Is(err, spreadsheet.ErrNotFound) {
			if expected == nil {
				return fmt.Errorf("artifact ID %s is occupied by another artifact", id)
			}
			return spreadsheet.ErrNotFound
		}
		if err != nil {
			return err
		}
		if expected == nil {
			return fmt.Errorf("spreadsheet %s already exists", id)
		}
		if !spreadsheet.Equal(current, *expected) {
			return fmt.Errorf("spreadsheet %s changed since this operation was recorded", id)
		}
		if target == nil {
			return w.deleteArtifactLocked(ctx, root, id, func() error {
				deleted, err := w.readSpreadsheetUnlocked(id)
				if err != nil {
					return err
				}
				if !spreadsheet.Equal(deleted, *expected) {
					return fmt.Errorf("spreadsheet %s changed before deletion", id)
				}
				return nil
			})
		}
		return w.saveSpreadsheetLocked(ctx, *target)
	})
}

func (w *Workspace) saveSpreadsheetLocked(ctx context.Context, book spreadsheet.Spreadsheet) error {
	if !validID.MatchString(book.ID) || book.Kind != artifact.SpreadsheetKind {
		return errors.New("invalid spreadsheet artifact")
	}
	if book.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", book.ID, spreadsheetName)) {
		return errors.New("invalid spreadsheet location")
	}
	data, err := spreadsheet.Encode(book)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.root, ".parchment", "artifacts", book.ID)
	created := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create spreadsheet storage: %w", err)
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil {
			return fmt.Errorf("inspect spreadsheet storage: %w", statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("spreadsheet storage path is not a directory")
		}
	} else {
		created = true
		if err := syncDirectory(filepath.Dir(dir)); err != nil {
			_ = os.Remove(dir)
			return fmt.Errorf("sync spreadsheet storage parent: %w", err)
		}
	}
	if err := recoverArtifactFiles(dir); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("recover spreadsheet files: %w", err)
	}
	if !created {
		if _, err := w.readSpreadsheetUnlocked(book.ID); err != nil {
			if errors.Is(err, spreadsheet.ErrNotFound) {
				return fmt.Errorf("artifact ID %s is occupied by another artifact", book.ID)
			}
			return fmt.Errorf("inspect existing spreadsheet before save: %w", err)
		}
	}
	if err := replaceArtifactFiles(ctx, dir, []stagedArtifactFile{{name: spreadsheetName, data: data}}); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save spreadsheet: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save spreadsheet: %w", err)
	}
	return nil
}

func (w *Workspace) readSpreadsheetUnlocked(id string) (spreadsheet.Spreadsheet, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spreadsheet.Spreadsheet{}, spreadsheet.ErrNotFound
		}
		return spreadsheet.Spreadsheet{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return spreadsheet.Spreadsheet{}, errors.New("spreadsheet storage path is not a directory")
	}
	data, err := readRegularFile(filepath.Join(dir, spreadsheetName), 32<<20)
	if errors.Is(err, os.ErrNotExist) {
		return spreadsheet.Spreadsheet{}, spreadsheet.ErrNotFound
	}
	if err != nil {
		return spreadsheet.Spreadsheet{}, fmt.Errorf("read spreadsheet %s: %w", id, err)
	}
	book, err := spreadsheet.Decode(data)
	if err != nil {
		return spreadsheet.Spreadsheet{}, fmt.Errorf("decode spreadsheet %s: %w", id, err)
	}
	if book.ID != id || book.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, spreadsheetName)) {
		return spreadsheet.Spreadsheet{}, fmt.Errorf("invalid spreadsheet metadata for %s", id)
	}
	return book, nil
}
