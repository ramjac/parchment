package docinterop

import (
	"fmt"
	"io"

	"example.com/parchment/internal/document"
)

// Warning is an error that represents a lossy conversion or approximation during import/export.
type Warning struct {
	Message string
}

func (w *Warning) Error() string {
	return fmt.Sprintf("warning: %s", w.Message)
}

// Importer defines the interface for converting external formats to a Parchment document draft.
type Importer interface {
	Import(r io.ReaderAt, size int64) (document.Draft, []error, error)
}

// Exporter defines the interface for converting a Parchment document to an external format.
type Exporter interface {
	Export(w io.Writer, doc document.Document) ([]error, error)
}
