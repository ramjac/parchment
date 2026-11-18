package tui

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/pdfrender"
)

type pdfExportResult struct {
	path    string
	err     error
	warning error
}

func pdfDefaultPath(source, lastPath string) string {
	if lastPath != "" {
		return lastPath
	}
	extension := filepath.Ext(source)
	if extension == filepath.Base(source) {
		extension = ""
	}
	return strings.TrimSuffix(source, extension) + ".pdf"
}

func writePDF(ctx context.Context, store PDFExportStore, path, source string, d document.Document, overwrite bool) pdfExportResult {
	var rendered bytes.Buffer
	if err := pdfrender.Render(&rendered, d); err != nil {
		return pdfExportResult{err: err}
	}
	if err := ctx.Err(); err != nil {
		return pdfExportResult{err: err}
	}
	if err := store.WritePDFExport(ctx, path, source, rendered.Bytes(), overwrite); err != nil {
		return pdfExportResult{path: path, err: err}
	}
	return pdfExportResult{path: path, warning: store.SavePDFPath(ctx, path)}
}
