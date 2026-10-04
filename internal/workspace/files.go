// Package workspace implements file-backed repositories that open individual
// artifact files wherever they live in the filesystem.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/spreadsheet"
)

const documentDataBlock = "parchment-document"

var errNotDocument = errors.New("artifact is not a document")

type embeddedDocumentImage struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type documentFileData struct {
	Layout  document.Layout         `json:"layout"`
	Changes []document.Change       `json:"changes,omitempty"`
	Images  []embeddedDocumentImage `json:"images,omitempty"`
}

type documentFileDataWithoutImages struct {
	Layout  document.Layout   `json:"layout"`
	Changes []document.Change `json:"changes,omitempty"`
}

func validateDocumentChanges(id string, changes []document.Change) error {
	seen := map[string]bool{}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return err
		}
		if change.DocumentID != id || seen[change.ID] {
			return errors.New("invalid document change list")
		}
		seen[change.ID] = true
	}
	return nil
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if limit < 0 {
		return io.ReadAll(file)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return os.Open(path)
}

// writeAtomic replaces path through a synced temporary file and a rename.
func writeAtomic(ctx context.Context, path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".parchment-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() {
		_ = os.Remove(temp)
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func cloneNote(n note.Note) note.Note {
	n.Blocks = cloneRawBlocks(n.Blocks)
	return n
}

func cloneSpreadsheet(b spreadsheet.Spreadsheet) spreadsheet.Spreadsheet {
	sheets := make([]spreadsheet.Sheet, len(b.Sheets))
	for i, sheet := range b.Sheets {
		rows := make([][]spreadsheet.Cell, len(sheet.Rows))
		for j, row := range sheet.Rows {
			rows[j] = append([]spreadsheet.Cell(nil), row...)
		}
		sheets[i] = spreadsheet.Sheet{Name: sheet.Name, Rows: rows}
	}
	b.Sheets = sheets
	b.Blocks = cloneRawBlocks(b.Blocks)
	return b
}

func cloneChange(c document.Change) document.Change {
	c.Before.ImageNames = append([]string(nil), c.Before.ImageNames...)
	c.After.ImageNames = append([]string(nil), c.After.ImageNames...)
	if c.ResolvedAt != nil {
		resolved := *c.ResolvedAt
		c.ResolvedAt = &resolved
	}
	return c
}

func cloneChanges(changes []document.Change) []document.Change {
	out := make([]document.Change, len(changes))
	for i, c := range changes {
		out[i] = cloneChange(c)
	}
	return out
}

func cloneRawBlocks(blocks map[string]json.RawMessage) map[string]json.RawMessage {
	if blocks == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(blocks))
	for name, payload := range blocks {
		out[name] = append(json.RawMessage(nil), payload...)
	}
	return out
}

func copyPayloadBlocks(blocks map[string]json.RawMessage, excluded string) map[string]json.RawMessage {
	var copied map[string]json.RawMessage
	for name, payload := range blocks {
		if name == excluded {
			continue
		}
		if copied == nil {
			copied = make(map[string]json.RawMessage, len(blocks))
		}
		copied[name] = append(json.RawMessage(nil), payload...)
	}
	return copied
}
