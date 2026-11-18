package filerepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const pdfPathStateFile = "last-pdf-path"
const maxPDFPathSize = 64 << 10

// LastPDFPath returns the most recent successful PDF destination.
func (r *Repository) LastPDFPath(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := readRegularFile(filepath.Join(r.stateDir, pdfPathStateFile), maxPDFPathSize)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read last PDF path: %w", err)
	}
	return string(data), nil
}

// SavePDFPath records an absolute PDF destination in application state.
func (r *Repository) SavePDFPath(ctx context.Context, path string) error {
	if path == "" || len(path) > maxPDFPathSize {
		return errors.New("invalid PDF output path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.withLock(ctx, func() error {
		if err := writeAtomic(ctx, filepath.Join(r.stateDir, pdfPathStateFile), []byte(abs), 0o600); err != nil {
			return fmt.Errorf("save last PDF path: %w", err)
		}
		return nil
	})
}

// WritePDFExport writes a PDF without replacing an existing file unless
// overwrite is true. It never replaces the source artifact.
func (r *Repository) WritePDFExport(ctx context.Context, path, source string, data []byte, overwrite bool) error {
	return r.withLock(ctx, func() error {
		target, err := resolve(path)
		if err != nil {
			return err
		}
		if !overwrite {
			if err := createAtomic(ctx, target, data, 0o600); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return fmt.Errorf("PDF file %q already exists: %w", path, fs.ErrExist)
				}
				return err
			}
			return nil
		}
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("inspect PDF file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("PDF file %q is not a regular file", path)
		}
		sourceInfo, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("inspect source file: %w", err)
		}
		if os.SameFile(info, sourceInfo) {
			return errors.New("PDF output cannot overwrite the source file")
		}
		return writeAtomic(ctx, target, data, info.Mode().Perm())
	})
}
