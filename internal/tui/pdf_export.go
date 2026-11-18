package tui

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/presentation"
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
	return writeRenderedPDF(ctx, store, path, source, overwrite, func(w io.Writer) error {
		return pdfrender.Render(w, d)
	})
}

func writePresentationPDF(ctx context.Context, store PDFExportStore, path, source string, item presentation.Presentation, overwrite, twoUp bool) pdfExportResult {
	return writeRenderedPDF(ctx, store, path, source, overwrite, func(w io.Writer) error {
		if twoUp {
			return pdfrender.RenderPresentationTwoUp(w, item)
		}
		return pdfrender.RenderPresentation(w, item)
	})
}

func writeRenderedPDF(ctx context.Context, store PDFExportStore, path, source string, overwrite bool, render func(io.Writer) error) pdfExportResult {
	var rendered bytes.Buffer
	if err := render(&rendered); err != nil {
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
