// Package workspace implements file-backed repositories that open individual
// artifact files wherever they live in the filesystem.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"example.com/parchment/internal/document"
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
func writeAtomic(path string, data []byte, mode os.FileMode) (err error) {
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
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
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
