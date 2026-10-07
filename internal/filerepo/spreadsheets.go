package filerepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/spreadsheet"
)

// GetSpreadsheet reads the spreadsheet stored at path.
func (r *Repository) GetSpreadsheet(ctx context.Context, path string) (spreadsheet.Spreadsheet, error) {
	if err := ctx.Err(); err != nil {
		return spreadsheet.Spreadsheet{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return spreadsheet.Spreadsheet{}, err
	}
	content, err := readContent(path, artifact.SpreadsheetKind, spreadsheet.ErrNotFound)
	if err != nil {
		return spreadsheet.Spreadsheet{}, err
	}
	return decodeSpreadsheet(path, content)
}

// TransitionSpreadsheet writes target to path only if the file still matches
// expected. A nil expected value creates a new file.
func (r *Repository) TransitionSpreadsheet(ctx context.Context, path string, expected, target *spreadsheet.Spreadsheet) error {
	if target == nil {
		return errors.New("Parchment does not delete artifact files")
	}
	if err := validateTarget(target.Artifact, artifact.SpreadsheetKind); err != nil {
		return err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, expected == nil, spreadsheet.ErrNotFound, func(content []byte) ([]byte, error) {
		if expected != nil {
			if err := checkKind(path, content, artifact.SpreadsheetKind); err != nil {
				return nil, err
			}
			current, err := decodeSpreadsheet(path, content)
			if err != nil {
				return nil, err
			}
			want := *expected
			want.Path = path
			if !spreadsheet.Equal(current, want) || target.ID != current.ID {
				return nil, changedError(path)
			}
		}
		data, err := spreadsheet.Encode(*target)
		if err != nil {
			return nil, fmt.Errorf("encode spreadsheet: %w", err)
		}
		return data, nil
	})
}

func decodeSpreadsheet(path string, content []byte) (spreadsheet.Spreadsheet, error) {
	book, err := spreadsheet.Decode(content)
	if err != nil {
		return spreadsheet.Spreadsheet{}, fmt.Errorf("decode %s: %w", path, err)
	}
	book.Path = path
	return book, nil
}
